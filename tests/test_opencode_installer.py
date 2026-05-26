"""Smoke tests for the OpenCode plugin installer.

The plugin is TypeScript embedded in a Python string. We can't run it from
pytest, but we can assert key APIs are wired up so a future refactor doesn't
silently lose behavior.
"""

from __future__ import annotations

from repowire.installers.opencode import PLUGIN_CONTENT


def assert_contains_all(*needles: str) -> None:
    for needle in needles:
        assert needle in PLUGIN_CONTENT


def test_prompt_and_injection_surface():
    """Queries and notifications use the non-blocking OpenCode APIs."""
    assert_contains_all(
        "session.promptAsync",
        "tui.prompt.append",
        "tui/publish",
    )
    assert "session.message({" not in PLUGIN_CONTENT
    assert "while (Date.now() - start < maxWait)" not in PLUGIN_CONTENT


def test_query_correlation_via_pending_map():
    """Pending queries correlate by userMessageId, with assistant ID discovered
    from message.updated parentID.
    """
    assert_contains_all(
        "pendingQueries",
        "messageID: userMessageId",
        "pendingByAssistantId",
        "trackAssistantMessage",
        "scheduleFlush",
        "flushPendingNow",
        "message.part.updated",
        "message.part.delta",
        "applyPartUpdated",
        "applyPartDelta",
        'props.field !== "text"',
        "textPartIds",
        '"session.status"',
        'turn_state = status === "busy" ? "working"',
        "payload.permission",
    )


def test_signal_handlers_exit():
    """SIGINT/SIGTERM handlers are one-shot and exit the process."""
    assert "process.once(\"SIGINT\"" in PLUGIN_CONTENT
    assert "process.once(\"SIGTERM\"" in PLUGIN_CONTENT
    assert "process.exit(130)" in PLUGIN_CONTENT


def test_per_session_registry_and_dispatch():
    """Session events route by sessionID to the matching PeerConn."""
    assert_contains_all(
        "peerBySession",
        "interface PeerConn",
        "ensurePeer",
        "removePeer",
        "session.created",
        "session.deleted",
        "info.parentID == null",
        "peerBySession.get(info.sessionID)",
    )
    assert "let primarySessionId" not in PLUGIN_CONTENT
    assert "resolvePrimarySession" not in PLUGIN_CONTENT


def test_concurrency_guard_per_peer():
    """The plugin rejects concurrent promptAsync calls on the same session."""
    assert "Session busy" in PLUGIN_CONTENT
    assert "conn.busy" in PLUGIN_CONTENT


def test_query_timeout_resets_busy():
    """A timed-out query must not leave the plugin-side peer busy forever."""
    assert "Query timed out waiting for OpenCode response" in PLUGIN_CONTENT
    timeout_idx = PLUGIN_CONTENT.index("Query timed out waiting for OpenCode response")
    reset_idx = PLUGIN_CONTENT.index("conn.busy = false", timeout_idx)
    idle_idx = PLUGIN_CONTENT.index('sendStatus(conn, "idle")', reset_idx)
    assert timeout_idx < reset_idx < idle_idx


def test_session_identity_surfaces():
    """Peer-id cache, tools, prompt context, and notifications use session identity."""
    assert_contains_all(
        "opencode-peer-ids.json",
        "cacheKey",
        "${projectPath}#${sessionId}",
        "callerPeer",
        "ctx.sessionID",
        "from_peer: me.peerName",
        "experimental.chat.system.transform",
        'You are peer "',
        "@${fromPeer} → ${conn.peerName}:",
    )


def test_no_session_id_hash_override():
    """Folder name is the stable display name; the old session-ID-hash override is gone."""
    assert "stableNameSet" not in PLUGIN_CONTENT
    assert "info.id.startsWith(\"ses\")" not in PLUGIN_CONTENT


def test_ask_reminder_polls_on_idle():
    """On session idle, plugin polls /asks/pending and softInjects a reminder."""
    assert "pollAndRemindPendingAsks" in PLUGIN_CONTENT
    # Polls inbound-only via peer_id (mirrors what claude-code/codex/gemini
    # Stop hooks emit on every turn close).
    assert "/asks/pending?peer_id=" in PLUGIN_CONTENT
    assert "direction=inbound" in PLUGIN_CONTENT
    # Wording matches hooks/ask_lifecycle.py:format_reminder_block so the
    # reminder block is consistent across all four backends.
    assert "[repowire] ${asks.length} open ask(s)" in PLUGIN_CONTENT
    assert "ack(corr_id) bare" in PLUGIN_CONTENT
    # Hook fires from both modern session.status idle and legacy session.idle.
    assert "void pollAndRemindPendingAsks(conn)" in PLUGIN_CONTENT


def test_permission_relay_hook_present():
    """permission.ask fires a notify to the telegram peer for relay."""
    assert '"permission.ask"' in PLUGIN_CONTENT
    assert "Permission request:" in PLUGIN_CONTENT
