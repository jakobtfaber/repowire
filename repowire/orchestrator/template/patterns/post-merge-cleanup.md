# Pattern: post-merge cleanup

After a PR merges and the work is complete: prune the worktree, kill the peer, verify the tmux pane is actually gone, update local main.

## When to reach for it

- A PR you dispatched merged successfully
- The peer that did the work is no longer needed (no continuation planned)
- Worktree was a per-feature `<project>.<feature>` clone, not the main project worktree

## When NOT to reach for it

- The peer has follow-up work queued (continuation turns)
- The worktree is the main project worktree, not a feature clone
- A parallel review is still verifying the merge — don't clean up until they ✅

## Shape

1. **Confirm merge state.**
   ```bash
   git fetch origin
   git log origin/main..HEAD  # should be empty on the feature branch
   ```
   If the branch has unpushed commits beyond what was merged, stop — investigate before destroying.

2. **Update the project's main worktree.**
   ```bash
   cd <project-main-worktree>
   git pull --rebase origin main
   ```

3. **Kill the peer in the feature worktree.**
   ```python
   mcp__repowire__kill_peer(name="<project>.<feature>-<runtime>")
   ```

4. **VERIFY the tmux pane is actually gone.** `kill_peer` clears mesh registration but does not always kill the underlying tmux window. Check:
   ```bash
   tmux list-windows -t <circle>
   ```
   If the window is still there:
   ```bash
   tmux kill-window -t <circle>:<window-name>
   ```
   Or by stable pane id (preferred when available):
   ```bash
   tmux kill-pane -t %<pane-id>
   ```

5. **Prune the worktree.**
   ```bash
   cd <project-main-worktree>
   git worktree remove <feature-worktree-path>
   git worktree prune
   git branch -d <feature-branch>  # local cleanup; -D if not merged-by-name
   ```

6. **Sanity-check cleanup.**
   ```bash
   git worktree list   # feature worktree should be gone
   tmux list-windows -t <circle>   # window gone
   mcp__repowire__list_peers()   # peer gone from mesh
   ```

## Anti-patterns

- **Skipping the tmux verify step.** `kill_peer` lies about pane death. Orphan tmux windows accumulate; eats memory; confuses later audits.
- **Pruning the worktree before confirming the pane is dead.** Orphan claude/codex process is then pointing at a deleted directory. Harmless functionally but pollutes state.
- **Cleaning up before a parallel review is done.** They need the worktree to verify against. Wait for ✅.

## When something looks wrong

- **Branch has unpushed commits not on main:** check `git log @{u}..HEAD` and `git ls-remote origin <branch>`. Could be abandoned work. Surface before destroying.
- **`kill_peer` returns 404:** peer was already deregistered (likely from a SessionEnd hook). Tmux pane may still be live; check anyway.
