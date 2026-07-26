from pathlib import Path

import pytest

from repowire.installers import runtime


def test_console_entrypoint_prefers_active_python_sibling(monkeypatch):
    monkeypatch.setattr(runtime.sys, "executable", "/uv/tools/repowire/bin/python")
    monkeypatch.setattr(
        runtime.sysconfig, "get_path", lambda name: "/other/python/bin"
    )
    calls = []

    def fake_which(command, *, path):
        calls.append((command, path))
        if path == "/uv/tools/repowire/bin":
            return "/uv/tools/repowire/bin/repowire"
        return "/contaminated/path/repowire"

    monkeypatch.setattr(runtime.shutil, "which", fake_which)

    assert runtime.repowire_console_entrypoint() == (
        str(Path("/uv/tools/repowire/bin/repowire").resolve())
    )
    assert calls == [("repowire", "/uv/tools/repowire/bin")]


def test_console_entrypoint_accepts_platform_executable_suffix(monkeypatch):
    monkeypatch.setattr(runtime.sys, "executable", "/tool/Scripts/python.exe")
    monkeypatch.setattr(runtime.sysconfig, "get_path", lambda name: "/tool/Scripts")
    monkeypatch.setattr(
        runtime.shutil,
        "which",
        lambda command, *, path: "/tool/Scripts/repowire.exe",
    )

    assert runtime.repowire_console_entrypoint().endswith("/tool/Scripts/repowire.exe")


def test_console_entrypoint_fails_instead_of_using_contaminated_path(monkeypatch):
    monkeypatch.setattr(runtime.sys, "executable", "/tool/bin/python")
    monkeypatch.setattr(runtime.sysconfig, "get_path", lambda name: "/tool/bin")
    monkeypatch.setattr(runtime.shutil, "which", lambda command, *, path: None)

    with pytest.raises(RuntimeError, match="active Python environment"):
        runtime.repowire_console_entrypoint()


def test_console_entrypoint_uses_trusted_absolute_argv0(tmp_path, monkeypatch):
    entrypoint = tmp_path / "repowire"
    entrypoint.write_text("#!/bin/sh\n")
    entrypoint.chmod(0o755)
    monkeypatch.setattr(runtime.sys, "executable", "/system/bin/python")
    monkeypatch.setattr(runtime.sys, "argv", [str(entrypoint)])
    monkeypatch.setattr(runtime.sysconfig, "get_path", lambda name: "/system/bin")
    monkeypatch.setattr(runtime.shutil, "which", lambda command, *, path: None)

    assert runtime.repowire_console_entrypoint() == str(entrypoint.resolve())


def test_console_entrypoint_rejects_non_executable_argv0(tmp_path, monkeypatch):
    entrypoint = tmp_path / "repowire"
    entrypoint.write_text("#!/bin/sh\n")
    entrypoint.chmod(0o644)
    monkeypatch.setattr(runtime.sys, "executable", "/system/bin/python")
    monkeypatch.setattr(runtime.sys, "argv", [str(entrypoint)])
    monkeypatch.setattr(runtime.sysconfig, "get_path", lambda name: "/system/bin")
    monkeypatch.setattr(runtime.shutil, "which", lambda command, *, path: None)

    with pytest.raises(RuntimeError, match="active Python environment"):
        runtime.repowire_console_entrypoint()
