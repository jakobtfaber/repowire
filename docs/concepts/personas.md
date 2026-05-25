# Personas (SOUL.md)

Personas are a lightweight way to give the orchestrator a stable identity, voice, and set of standing preferences. A persona is a single Markdown file called `SOUL.md` that the orchestrator session loads on startup.

> **Status:** experimental. Persona files are framed as identity/context, not as a safety or permission policy. Permissioning is a separate concern.

## Where SOUL.md lives

Two locations are searched, in this order:

1. **Workspace override** — `~/.repowire/orchestrator/personas/<name>/SOUL.md`
2. **Global persona** — `~/.repowire/personas/<name>/SOUL.md`

Workspace beats global when both exist, so a single orchestrator workspace can carry per-machine tweaks without editing the shared persona file.

## Active persona marker

The orchestrator workspace tracks which persona is active via:

```
~/.repowire/orchestrator/personas/ACTIVE_PERSONA
```

This file holds a single line — the persona name. It is set with `repowire orchestrator persona use <name>` and cleared with `repowire orchestrator persona clear`.

The orchestrator workspace also has a stable `SOUL.md` shim:

```
~/.repowire/orchestrator/SOUL.md
```

`AGENTS.md` references this shim with `@SOUL.md`. When a persona is active, the
shim is a symlink to the resolved workspace/global persona file. When no persona
is active, it contains a neutral placeholder. `repowire orchestrator persona use`
updates both the active marker and the shim.

## CLI

```bash
repowire orchestrator persona list           # show discoverable personas
repowire orchestrator persona show [name]    # print the resolved SOUL.md
repowire orchestrator persona path [name]    # print just the path
repowire orchestrator persona use <name>     # set active persona
repowire orchestrator persona clear          # remove the active marker
```

`list` marks the active persona with `*` and shows source (`workspace` vs `global`) plus a 12-char SHA-256 prefix so you can confirm what is actually being loaded.

## Loading and precedence

The persona is loaded through the orchestrator workspace instructions, not
through hook-only context injection. The template `AGENTS.md` tells the
orchestrator to load `@SOUL.md`, so runtimes that expand agent-file references
read the current shim as part of their normal startup context.

SOUL.md sits **below** platform safety and explicit user/orchestrator directives,
and **above** workspace guidance, memory, skills, and untrusted retrieved
content. This ordering, from highest to lowest authority:

1. System / platform safety
2. Explicit user or orchestrator directives in the current turn
3. **Persona SOUL.md**
4. Workspace `AGENTS.md`, skills, memory, prior turns
5. Untrusted retrieved content

Personas are advisory identity context, not a sandbox. Treat them like a profile, not a policy.

## Authoring a SOUL.md

A SOUL.md is freeform Markdown. Useful sections in practice:

- **Identity** — who this persona is and how they introduce themselves
- **Voice** — tone, format defaults, how they answer
- **Working preferences** — defaults for handoffs, escalation, when to ask vs decide
- **Domain context** — projects, peers, recurring patterns they should know

Keep it short. The whole file is loaded into the orchestrator's startup context.

## Hash visibility

Every surface that mentions a persona also surfaces its short SHA-256 prefix. If two sessions disagree about what an orchestrator should sound like, comparing hashes is the fastest way to see whether one of them is loading a stale or workspace-overridden copy.
