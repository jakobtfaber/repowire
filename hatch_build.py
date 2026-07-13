"""Build hook: bundle the web UI and the Go hub binary when each is present.

- web/out: prebuilt dashboard. Ships in release builds, absent in clean dev
  checkouts. A static force-include would error on the missing path and break
  `uv sync` / editable installs (#208), so inclusion is conditional.
- repowire/_bin/repowire-hub-go: the Go daemon hub binary. CI cross-compiles it
  per target into that path before `uv build`; local source wheels build it for
  the current host. The wheel is marked platform-specific (tag from
  $REPOWIRE_WHEEL_TAG, else inferred from the build host). Editable installs
  build the Go command into the user cache on first invocation.
"""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

from hatchling.builders.hooks.plugin.interface import BuildHookInterface


class AssetsHook(BuildHookInterface):
    PLUGIN_NAME = "web-out"

    def initialize(self, version: str, build_data: dict) -> None:
        root = Path(self.root)

        web_out = root / "web" / "out"
        if web_out.is_dir() and any(web_out.iterdir()):
            build_data.setdefault("force_include", {})[str(web_out)] = "web/out"

        hub_bin = root / "repowire" / "_bin" / "repowire-hub-go"
        source = root / "daemon-go"
        if (
            self.target_name == "wheel"
            and not os.environ.get("REPOWIRE_WHEEL_TAG")
            and (source / "go.mod").is_file()
            and shutil.which("go")
        ):
            hub_bin.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(["go", "build", "-o", hub_bin, "."], cwd=source, check=True)
        if hub_bin.is_file():
            build_data.setdefault("force_include", {})[str(hub_bin)] = (
                "repowire/_bin/repowire-hub-go"
            )
            # A bundled native binary makes the wheel platform-specific.
            build_data["pure_python"] = False
            tag = os.environ.get("REPOWIRE_WHEEL_TAG")
            if tag:
                build_data["tag"] = tag
            else:
                build_data["infer_tag"] = True
