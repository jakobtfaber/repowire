# Antigravity CLI (`agy`)

Google's [Antigravity](https://antigravity.google) CLI, distinct from Gemini CLI. Separate binary (`agy`), separate auth, separate app-data directory (`~/.gemini/antigravity-cli/`).

## What gets installed

Repowire ships a plugin into the Antigravity plugins directory at `~/.gemini/antigravity-cli/plugins/repowire/` and registers it in `import_manifest.json`. The plugin layout has been verified with `agy plugin validate`:

```
~/.gemini/antigravity-cli/plugins/repowire/
├── plugin.json          # {"name": "repowire", "version": "...", ...}
└── hooks/
    └── hooks.json       # SessionStart / BeforeAgent / AfterAgent
```

Hook events mirror Gemini's naming, since `agy` and Gemini CLI share the same plugin schema for hooks. The adapter at `hooks/adapters.py` already normalises `BeforeAgent`/`AfterAgent` to canonical names.

| Antigravity event | Mapped to | Command |
| --- | --- | --- |
| `SessionStart` (matcher: `startup`) | session hook | `repowire hook session --backend=antigravity` |
| `BeforeAgent` | prompt hook | `repowire hook prompt --backend=antigravity` |
| `AfterAgent` | stop hook | `repowire hook stop --backend=antigravity` |

## Pending upstream verification

The Antigravity CLI plugin layout validates cleanly, but two pieces of end-to-end behaviour are not yet confirmed and should be treated as best-effort:

1. **Whether `agy` actually fires plugin-defined hooks at session boundaries today.** `agy --help` does not document the hook system, and the plugin validator reports `hooks: 1 processed` without running them. `repowire status` and `repowire doctor` mark this gap as a `WARN`, not an `OK`.
2. **MCP server registration via the plugin system.** The `mcpServers/` plugin subdirectory shape couldn't be verified through `agy plugin validate`. The installer deliberately does **not** write any MCP entries — writing unknown shapes is worse than an honest gap. `check_mcp_installed()` always returns `False` for Antigravity until the schema is documented or observed.

When upstream lands hook/MCP support, the installer will be tightened to write the verified shapes and `doctor` will flip from `WARN` to `OK` without changes to the surrounding integration.

## Spawn

Default spawn command: `agy --dangerously-skip-permissions`.

```yaml
daemon:
  spawn:
    commands:
      antigravity: "agy --dangerously-skip-permissions"
```

## Verifying

```bash
repowire status     # ⚠ antigravity (plugin installed; hook firing pending upstream)
repowire doctor     # WARN: vX.Y.Z — plugin installed; hook firing/MCP pending upstream
agy plugin list     # should show repowire under "imports"
```

## Uninstall

`repowire uninstall` removes the plugin directory and manifest entry. Equivalent manual cleanup:

```bash
agy plugin uninstall repowire
```
