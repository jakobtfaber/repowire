---
name: project-docs-ia-plan
description: Docs/ IA re-arrange shipped in #348 — 6 grouped lanes (Use/Operate/Support), guides+capabilities merged into Features, patterns kept as Workflows. Rationale record.
metadata:
  type: project
---

Docs IA re-arrange agreed 2026-06-02 (Prass + repowire-codex critique + claude-code). SHIPPED in PR #348 (main bf883b2) — codex implemented, claude-code reviewed+merged. Record kept for the rationale.

Pain: 9 top-level nav sections, same feature split across guides/ + capabilities/ + concepts/ (dashboard, scheduling, transports all duplicated). Reader optimized for = active user.

Shipped IA (6 grouped lanes):
- Start (unchanged)
- Use → Features (guides/ + capabilities/ MERGED, one authoritative page per feature, with a Connect subsection) + Workflows (was patterns/, kept intact — codex argued patterns are active-user recipes, not theory, so NOT folded under Concepts)
- Concepts (mental models only; control-surfaces + transports keep the invariant/model, ZERO feature lists)
- Reference (unchanged)
- Operate (was operations/; transports page → runbook altitude vs Concepts/transports model altitude)
- Support → Troubleshooting + Contributing

Key discipline (the part that actually killed the seam): each Feature page got a standard contract — what it is / when to use / setup / common workflows / commands+API / limits / troubleshoot / see-also. The merge REWROTE to this shape, not concatenated guide+capability.

Decisions made: NO mkdocs-redirects (Prass's call) — external inbound links to old capabilities/guides URLs will 404, accepted. capabilities/skills.md was nav'd under Use/Features/Skills (it had been an orphan). Builder is zensical (`zensical build --strict`), not mkdocs. See [[feedback_check_branch_before_commit]] for a process lesson from this same session.
