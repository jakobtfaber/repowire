---
name: feedback-check-branch-before-commit
description: When a mesh peer is implementing on a shared branch, check git branch --show-current before committing local edits — your checkout may have followed their branch.
metadata:
  type: feedback
---

When delegating implementation to a mesh peer (e.g. codex on branch docs/ia-use-operate-support) and also making a small local edit (README badge), check `git branch --show-current` BEFORE `git commit`. The local checkout can silently be on the peer's feature branch, so commits land on their branch instead of a clean base.

**Why:** On 2026-06-02 the README badge + memory commits landed on codex's docs branch by accident. It resolved harmlessly because the PR was squash-merged (everything collapsed into one main commit), but it could have tangled an unrelated change into a feature PR or caused a reset/rebase clash.

**How to apply:** Before committing any standalone edit while a peer owns a branch, confirm the current branch. If it's the peer's branch and the edit is unrelated, branch off main first (`git checkout -b ... main`) or hold the edit until after their PR merges. See [[project_docs_ia_plan]].
