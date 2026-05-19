# Hermes Memory and Skills: Repowire Architecture Read

Date: 2026-05-20

Scope: research/spec only. This compares Hermes Agent's memory and skill-manager ideas against Repowire's current architecture: local daemon + transport adapters, MCP/direct tool surfaces, orchestrator workspace at `~/.repowire/orchestrator/`, AGENTS/template conventions, dashboard/session timeline direction, and third-party skill/plugin ecosystems.

## Sources reviewed

- Hermes docs:
  - Skills: `https://hermes-agent.nousresearch.com/docs/user-guide/features/skills#agent-managed-skills-skill_manage-tool`
  - Memory: `https://hermes-agent.nousresearch.com/docs/user-guide/features/memory`
- Repowire local surfaces:
  - `README.md`
  - `docs/reference/architecture.md`
  - `docs/concepts/session-native-roadmap.md`
  - `docs/surfaces/dashboard.md`
  - `docs/agents/claude-code.md`
  - `docs/reference/mcp-tools.md`
  - `repowire/orchestrator/workspace.py`
  - `repowire/orchestrator/template/AGENTS.md`
  - `repowire/orchestrator/template/memory/MEMORY.md`
  - `repowire/session/history.py`
  - Pi extension notes in `repowire/installers/pi.py`

## Executive read

Hermes is building memory and skills as first-class agent capabilities: bounded prompt-injected memory, session search, progressive-disclosure skills, skill bundles, external skill directories, marketplace installs, security scanning, and an agent-managed `skill_manage` tool for procedural memory.

Repowire should not copy this as a general agent runtime. Repowire's product boundary is an orchestrator mesh: live sessions, routing, timelines, and control surfaces across multiple runtimes. The useful borrow is therefore narrower:

1. Make orchestrator memory more bounded, inspectable, and session-aware.
2. Treat recurring orchestration patterns as progressive-disclosure capabilities, not always-in-prompt bulk.
3. Provide safe discovery/launch points for existing skill ecosystems without becoming a skill marketplace immediately.
4. Attach memory/procedure changes to the future session timeline so users can audit what the orchestrator learned and why.

## 1. Useful ideas to borrow now

### A. Bounded, explicit orchestrator memory budget

Hermes uses two small stores:

- `MEMORY.md` for environment/workflow facts.
- `USER.md` for user preferences.

Both have hard character limits and are injected as a frozen session-start snapshot. Repowire's orchestrator already has `memory/MEMORY.md` plus one-file-per-lesson entries, but there is no product-enforced budget or visible capacity signal.

Borrow now:

- Add conventions or tooling for a bounded orchestrator memory index.
- Separate user preference memory from operational lessons:
  - `comms.md` and maybe `user.md` for preferences.
  - `memory/*.md` for operational lessons.
- Show memory size/capacity in orchestrator health/check output, even before enforcing limits.
- Prefer consolidation over unlimited growth.

Why it fits: the orchestrator is long-running and coordination-heavy. Its memory has real prompt/token cost and can drift into stale folklore.

### B. Frozen snapshot semantics

Hermes deliberately injects memory once at session start and does not mutate the prompt mid-session. Tool responses may show live state, but the model's active prefix remains stable.

Borrow now:

- Document the orchestrator memory model as session-start context, not live magical memory.
- In future resume/session work, record which memory snapshot was loaded for a session.
- Avoid designs where memory edits immediately rewrite active context across peers.

Why it fits: Repowire's dashboard and timeline direction benefits from reproducibility: "this session acted with memory snapshot X" is auditable.

### C. Progressive disclosure for patterns/skills

Hermes skills use:

- Level 0: skill index (`name`, `description`, `category`).
- Level 1: full `SKILL.md`.
- Level 2: supporting reference files.

Repowire's orchestrator template already points to patterns on demand (`patterns/spawn-brief-iterate.md`, `two-model-critique.md`, etc.), but currently this is mostly prose in `AGENTS.md`.

Borrow now:

- Treat orchestrator `patterns/` as a local skill-like library.
- Keep only an index in `AGENTS.md`; load pattern files when needed.
- Add frontmatter to pattern files over time: `name`, `description`, `triggers`, `risk`, `surfaces`.
- Make dispatch briefs cite pattern/memory refs explicitly.

Why it fits: Repowire should optimize for routing judgment. Progressive disclosure keeps the orchestrator prompt small while preserving high-quality procedures.

### D. Agent-managed procedural memory, but scoped to the orchestrator workspace

Hermes' `skill_manage` tool lets the agent create/update/delete skills after it discovers a reusable workflow. Repowire already tells the orchestrator to evolve `memory/`, `patterns/`, `comms.md`, and `projects.md` directly.

Borrow now conceptually, not as a general tool:

- Productize a narrow "propose memory/pattern update" flow for the orchestrator.
- Prefer patch-style updates over full rewrites.
- Require user-visible rationale: what changed, why, and where it will apply next time.
- Keep writes inside `~/.repowire/orchestrator/`, not arbitrary user/project skill directories.

Why it fits: this matches Repowire's orchestrator as a coordination learner without granting broad write access to global agent behavior.

### E. Session search as the complement to memory

Hermes distinguishes:

- Memory: tiny, always-on, curated facts.
- Session search: on-demand full-text search of past conversations.

Repowire already has early session history surfaces (`repowire/session/history.py`, dashboard per-peer chat, Claude transcript replay). This is highly aligned with v0.13 session-native work.

Borrow now:

- Keep orchestrator memory small because session/timeline search should carry bulky recall.
- Design future timeline search as the answer to "what did we discuss last week?" rather than putting diary entries into memory.
- Add session IDs and memory snapshot references to future timeline records.

### F. Secure setup pattern for skills/config

Hermes skills can declare required env vars and non-secret config. Secrets are requested only in local CLI setup, not in chat surfaces.

Borrow now for any future Repowire-managed capabilities:

- Never ask for tokens in Telegram/dashboard chat.
- If a skill/pattern/tool needs secrets, route setup through local CLI/config.
- Keep capability config explicit and inspectable under `~/.repowire/config.yaml` or a scoped orchestrator config file.

This matches Repowire's local-first security posture and relay constraints.

## 2. Ideas to defer or avoid

### Avoid: Repowire as a general skill marketplace in v0.13

Hermes integrates official skills, skills.sh, well-known endpoints, GitHub taps, direct URLs, community marketplaces, scans, lockfiles, updates, audits, and publishing. That is a whole product surface.

Repowire should avoid this now because:

- Existing ecosystems already serve Claude Code/Codex/Pi skill packaging.
- Repowire's unique value is mesh routing/session control, not capability distribution.
- Marketplace installation would require substantial trust policy, update lifecycle, provenance tracking, docs, uninstall behavior, and UI.

Near-term stance: interoperate and document; do not own.

### Defer: agent-managed arbitrary skills

A general `skill_manage` equivalent that can create/update/delete global skills is too broad for Repowire's trust boundary.

Risks:

- Prompt-injection persistence.
- Cross-runtime behavior changes the user did not approve.
- Supply-chain confusion with third-party skill installers.
- Hard-to-debug changes to agent behavior outside Repowire's daemon/mesh scope.

If Repowire adds this later, it should be opt-in, scoped, reviewed, and timeline-audited.

### Defer: external memory providers

Hermes supports external memory providers for semantic search/knowledge graphs/user modeling. Repowire should not integrate these before the session timeline/search foundation is solid.

Reason: Repowire already has first-party raw material: mesh events, asks/acks, chat turns, schedules, peer lifecycle, transcripts. Build durable session storage and search first. External memory can be a later provider interface if users ask for deeper recall.

### Avoid: memory as hidden global behavior

Memory should not silently alter every peer. In a mesh, hidden global memory can create inconsistent behavior across agents/runtimes and make routing failures hard to debug.

Prefer:

- Orchestrator-scoped memory.
- Project-local AGENTS/context files.
- Explicit brief attachments/references when dispatching peers.
- Timeline-visible memory changes.

### Defer: skill bundles as user-facing slash commands across all surfaces

Hermes bundles are useful, but Repowire's current surface is MCP/direct tools, dashboard, Telegram, Slack, and daemon routes. Slash-command dispatch across all surfaces would create compatibility and parsing questions.

For v0.13, the comparable concept should be "orchestrator patterns" or "dispatch profiles", not universal skill bundles.

## 3. Product architecture fit for Repowire as an orchestrator mesh

### Repowire's boundary

Repowire is a local-first routing daemon plus adapters. It connects active sessions and human surfaces. Current stable concepts are peers, circles, asks, acks, notifications, broadcasts, schedules, and dashboard timeline events.

Memory/skills should therefore attach to coordination, not replace agent-native systems:

- **Daemon:** owns routing, identity, events, schedule delivery, session/timeline storage.
- **Transports/adapters:** expose routing tools via MCP, Pi direct tools, OpenCode plugin, hooks/channel.
- **Orchestrator workspace:** owns mesh-specific operating memory and dispatch procedures.
- **Agent runtimes:** keep their native skill/plugin systems.

### Recommended fit

A Repowire-native memory/skill layer should be framed as:

> Orchestrator operating knowledge: small, auditable instructions that help a coordinator route work, brief peers, review results, and resume sessions.

Not:

> A universal skill manager for every agent runtime.

### Concept mapping

| Hermes concept | Repowire analogue | Fit |
| --- | --- | --- |
| `MEMORY.md` bounded personal notes | `~/.repowire/orchestrator/memory/` | Strong, but needs budget/consolidation/audit |
| `USER.md` profile | `comms.md` / possible `user.md` | Strong, should stay human-readable |
| `session_search` | v0.13 session timeline/history/search | Strong, should be core roadmap input |
| `SKILL.md` progressive disclosure | orchestrator `patterns/*.md` / future dispatch profiles | Strong for coordination procedures |
| `skill_manage` tool | propose/patch orchestrator memory/pattern files | Medium; scope tightly |
| skill hub/marketplace | third-party Claude/Codex/Pi ecosystems | Weak near-term; interop only |
| skill bundles | orchestrator pattern bundles/dispatch profiles | Medium later |
| secure env setup | local config/setup, not chat | Strong |
| external dirs | read-only shared team procedures | Medium later, after trust model |

### Where Pi/direct tools matter

Repowire supports Pi through an extension and direct registered tools, not MCP. Any future memory/pattern tool should not assume MCP-only exposure. The surface matrix must include:

- MCP server tools (`repowire/mcp/server.py`).
- Pi extension direct tools (`repowire/installers/pi.py`).
- OpenCode plugin if relevant.
- CLI commands.
- Dashboard controls.
- Telegram/Slack command paths if user-facing.

This argues for daemon-backed APIs first, with thin adapters per runtime, matching the current architecture.

## 4. Concrete v0.13-compatible PR slices

These slices preserve current hooks/MCP/HTTP behavior and align with the v0.13 session-native train. They are spec candidates, not implementation approval.

### Slice 1: Orchestrator memory audit command

Add a non-invasive CLI command such as:

```bash
repowire orchestrator memory status
```

Possible output:

- Workspace path.
- Count of memory files.
- Index size.
- Largest entries.
- Missing index links.
- Entries over recommended size.
- Potential duplicate titles.

No daemon changes required. No product behavior change beyond diagnostics.

Docs impact: CLI reference and orchestrator docs if public.

### Slice 2: Memory budget convention in orchestrator template

Update `repowire/orchestrator/template/memory/MEMORY.md` and/or `AGENTS.md` to include recommended budgets and consolidation rules.

Example guidance:

- Keep `memory/MEMORY.md` as a compact index.
- Keep each memory file under a soft character limit.
- Consolidate above N entries or when lessons overlap.
- Keep user preferences in `comms.md`, not operational memory.

This is compatible with current architecture and requires no runtime feature.

Docs impact: because generated templates change public behavior for orchestrator users, update orchestrator docs/references.

### Slice 3: Pattern frontmatter/index convention

Add optional metadata to shipped orchestrator pattern files:

```yaml
---
name: spawn-brief-iterate
description: Default dispatch loop for new work
triggers: [new-work, peer-dispatch, review]
risk: medium
---
```

Then update `AGENTS.md` to describe patterns as progressive-disclosure procedures.

No parsing required in first PR; the value is convention and future compatibility.

Docs impact: orchestrator pattern docs.

### Slice 4: Session timeline design note for memory snapshots

Add a design doc or expand `docs/concepts/session-native-roadmap.md` with the principle:

- Sessions should eventually record the context/memory snapshot used at start/resume.
- Memory changes should be timeline events.
- Timeline search should be the long-tail recall mechanism, not prompt memory.

No code required.

Docs impact: roadmap/concepts only.

### Slice 5: Read-only skill ecosystem interop docs

Add a short public doc section clarifying Repowire's stance:

- Repowire does not install third-party skills today.
- Existing skill/plugin ecosystems can be used alongside Repowire.
- Mesh coordination procedures live in the orchestrator workspace.
- If future Repowire skill packaging lands, it will need trust/provenance/audit.

Some of this exists in `docs/agents/claude-code.md`, `docs/quickstart/install.md`, and `README.md`; this slice would centralize the conceptual boundary.

### Slice 6: Future API sketch only — `orchestrator_memory` / `orchestrator_pattern` tools

Spec only; no implementation yet.

Potential tool shape:

```python
orchestrator_memory(action="propose", target="memory|comms|project", content="...", rationale="...")
orchestrator_pattern(action="list|view|propose_patch", name="...", ...)
```

Important constraints:

- Available only to peers with `role=orchestrator` or local CLI.
- Writes are staged/proposed by default.
- Final apply is local and auditable.
- All changes become timeline events.
- Adapters must include MCP and Pi direct-tool surfaces if promoted to product.

## 5. Security and trust concerns for agent-managed skills

Agent-managed skills are persistent prompt/code supply chain. Treat them as higher risk than normal chat output.

### Main risks

1. **Persistent prompt injection**
   - A malicious web page, issue, PR, or peer message could convince the agent to save hostile instructions into future memory/skills.

2. **Credential exfiltration**
   - Skills/scripts may embed commands that read env vars, config files, SSH keys, cloud tokens, or local files.

3. **Privilege escalation through workflow automation**
   - A skill can normalize destructive commands or unsafe approval bypass flags.

4. **Cross-surface surprise**
   - A skill created from Telegram might affect later Claude Code, Codex, or Pi sessions in a different repo.

5. **Unreviewed third-party code**
   - Marketplace or URL-installed skills can include scripts/assets/references that are harder to audit than a single markdown instruction.

6. **Stale or conflicting procedures**
   - Mesh coordination rules rot quickly; stale rules cause misroutes, bad cleanup, or unsafe release behavior.

7. **Path/secrets leakage in public repo memory**
   - Repowire has public in-repo memory under `.claude/memory/` with sanitization rules. Any memory tooling must distinguish public project memory from private orchestrator memory.

### Guardrails if Repowire ever adds managed skills

- Scope writes to `~/.repowire/orchestrator/` by default.
- Require explicit user approval for global skill/plugin installs.
- Make proposed changes diff-first, not silent writes.
- Store provenance: source session, peer, timestamp, triggering user correction, and files changed.
- Block invisible Unicode and obvious prompt-injection/exfiltration patterns.
- Never request secrets through dashboard/Telegram/Slack chat.
- Keep external directories read-only unless explicitly configured.
- Prefer patch operations to full rewrites.
- Add an audit command that lists recent memory/pattern changes.
- Add timeline events for memory/pattern modifications.
- Keep uninstall/reset behavior clear.

## 6. Relationship to future resume/session/timeline work

Hermes' strongest idea for Repowire is not the skill hub; it is the separation between compact memory and searchable sessions.

Repowire's v0.13 direction says sessions become durable units while peers remain live executors. Memory/skills should plug into that model:

### Resume

When resuming a session, Repowire should eventually know:

- Which peer/runtime is executing now.
- Which durable session/workstream it belongs to.
- Which memory/pattern snapshot was used.
- What changed since the prior run.

This avoids "stale peer killed, context lost" as the only lifecycle option and supports safer reattach/resume flows.

### Timeline

Memory and procedural updates should be first-class timeline events:

- "Orchestrator proposed memory update: prefer notify over ask for dispatch."
- "User approved pattern update: two-model critique threshold."
- "Session resumed with memory snapshot hash X."

This makes learned behavior inspectable and reversible.

### Search

Timeline/session search should handle long-tail recall:

- Past discussions.
- Detailed task history.
- Logs of asks/acks and peer decisions.
- Why a release was held or shipped.

Memory should only hold durable rules that change future behavior.

### Dispatch profiles

Future "skills" in Repowire should likely look like dispatch profiles:

- Which pattern(s) to load.
- Which memories are relevant.
- Which peer/runtime to prefer.
- Which review gates apply.
- Which surface to notify.

This is more mesh-native than generic `SKILL.md` installation.

## Recommended product stance

Short version:

- Borrow Hermes' bounded memory discipline, progressive disclosure, secure setup posture, and memory-vs-session-search separation.
- Do not build a skill marketplace in v0.13.
- Keep agent-managed procedural memory scoped to the orchestrator workspace, diffed, auditable, and eventually timeline-native.
- Let Claude Code/Codex/Pi/native ecosystems own generic skills and plugins; Repowire should own coordination procedures and session continuity.
