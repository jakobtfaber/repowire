"""Hardened tmux paste injection, shared across transports.

A single tmux-paste implementation used by both the ws-hook (client-side,
normal message delivery) and the daemon's post-spawn seed (server-side wake-up
for backends whose hook registers lazily). Two near-duplicate implementations
existed before — the hook's was hardened against the runtime's bracketed-paste
heuristic swallowing the submit Enter, the spawn-seed one was not — and a seed
message into a freshly booted pane was injected through the un-hardened path.

The pattern (Gastown's battle-tested NudgeSession):
1. If pane is in copy-mode, cancel out first (otherwise ``-l`` lands in the
   copy buffer and Enter triggers a selection action instead of submit).
2. Send text in literal mode (bracketed paste).
3. Debounce, scaled with text length — the runtime's paste heuristic must
   settle before Enter or it swallows it as a newline.
4. Explicitly close bracketed paste mode with ESC[201~.
5. Enter — submits.
6. Verify after every Enter: while the composer still holds the text, the
   paste heuristic ate the submit — retry within a fixed bound. Stop as soon
   as the composer is empty, and never retry when capture evidence is absent.
"""

from __future__ import annotations

import logging
import re
import subprocess
import time
from enum import Enum

logger = logging.getLogger(__name__)

# tmux composer prompt glyphs across the supported runtimes. The bottom-most
# line starting with one of these is the live input line.
_COMPOSER_PROMPT_PREFIXES = ("❯", "›", "> ")

# A positively live composer must remain unchanged across consecutive full-pane
# captures and must not declare ready before this startup floor. A bare composer
# can coexist with Claude's busy compaction UI, so the glyph alone is unsafe.
STABLE_READY_FLOOR_SECONDS = 5.0

# Claude Code 2.1.220 has been observed swallowing two consecutive submit
# Enters for a long seed prompt. Keep retries bounded while allowing one
# additional attempt beyond that observed sequence.
_MAX_SUBMIT_ATTEMPTS = 3
_SUBMIT_VERIFY_POLLS = 8
_SUBMIT_STABLE_HOLDS = 6
_SUBMIT_POLL_SECONDS = 0.2


class _ComposerState(Enum):
    HOLDS = "holds"
    CLEARED = "cleared"
    UNKNOWN = "unknown"


_ACTIVITY_GLYPHS = "✢✣✤✥✦✧✳✶✻✽"
_BUSY_PERCENT_RE = re.compile(r"(?<!\d)\d{1,3}%")
_PROGRESS_BAR_RE = re.compile(r"^[█▓▒░▏▎▍▌▋▊▉━▰▱-]+\s*\d{1,3}%")


def _pane_in_copy_mode(pane_id: str) -> bool:
    """True if the pane is in tmux copy-mode. False on any query failure."""
    try:
        result = subprocess.run(
            ["tmux", "display-message", "-t", pane_id, "-p", "#{pane_in_mode}"],
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False
    if result.returncode != 0:
        return False
    return result.stdout.strip() == "1"


def _capture_pane(pane_id: str) -> str | None:
    """Capture visible pane text, or None on any failure."""
    try:
        result = subprocess.run(
            ["tmux", "capture-pane", "-t", pane_id, "-p", "-J", "-S", "-30"],
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None
    if result.returncode != 0:
        return None
    return result.stdout


def _composer_prompt_present(pane_text: str) -> bool:
    """True if the captured pane shows a positively live composer block."""
    return _composer_block_text(pane_text) is not None


def _idle_composer_present(pane_text: str) -> bool:
    """True only for an empty live composer without nearby busy evidence."""
    if _composer_block_text(pane_text) != "":
        return False
    for line in pane_text.splitlines()[-12:]:
        stripped = line.strip()
        activity_text = stripped.lstrip(_ACTIVITY_GLYPHS).strip()
        if activity_text.lower().startswith("compacting conversation"):
            return False
        if (
            stripped.startswith(tuple(_ACTIVITY_GLYPHS))
            and ("…" in stripped or "..." in stripped or _BUSY_PERCENT_RE.search(stripped))
        ):
            return False
        if _PROGRESS_BAR_RE.match(stripped):
            return False
    return True


def _composer_block_text(pane_text: str) -> str | None:
    """Return normalized text from a positively live composer prompt block.

    Claude frames its active composer with a closing divider. Simpler runtimes
    may expose only a final prompt line. A prompt followed by non-divider
    output is transcript history or processing state, not a live composer.
    """
    lines = pane_text.splitlines()
    for index in range(len(lines) - 1, -1, -1):
        stripped = lines[index].lstrip()
        prompt_prefix = next(
            (
                prefix
                for prefix in _COMPOSER_PROMPT_PREFIXES
                if stripped.startswith(prefix)
            ),
            None,
        )
        if prompt_prefix is None:
            continue
        content_lines = [stripped[len(prompt_prefix) :].strip()]
        saw_closing_divider = False
        for continuation in lines[index + 1 :]:
            divider = continuation.strip()
            if divider and set(divider) == {"─"}:
                saw_closing_divider = True
                break
            content_lines.append(continuation.strip())
        if not saw_closing_divider and any(content_lines[1:]):
            return None
        return " ".join(" ".join(content_lines).split())
    return None


def _composer_state(pane_id: str, text: str) -> _ComposerState:
    """Classify the injected prompt as held, cleared, or unknown.

    A submitted prompt also remains visible in the transcript, so presence of
    the text alone proves nothing. The distinguishing feature is the
    bottom-most composer prompt line: after a successful submit it is empty
    (a bare prompt), while a swallowed Enter leaves our text in it. Joined
    capture lines keep wrapped long prompts in one logical composer line.

    Capture failures, missing prompt glyphs, and empty injected text are
    unknown. They must never justify another Enter.
    """
    pane_text = _capture_pane(pane_id)
    if pane_text is None:
        return _ComposerState.UNKNOWN
    expected_text = " ".join(text.split())
    if not expected_text:
        return _ComposerState.UNKNOWN
    composer_text = _composer_block_text(pane_text)
    if composer_text is None:
        return _ComposerState.UNKNOWN
    if composer_text == expected_text:
        return _ComposerState.HOLDS
    if not composer_text:
        return _ComposerState.CLEARED
    return _ComposerState.UNKNOWN


def _wait_for_submit_state(pane_id: str, text: str) -> _ComposerState:
    """Observe a bounded window before declaring the composer held.

    A transient capture failure is inconclusive, not terminal. Only a run of
    exact held-composer observations justifies another Enter. Unknown resets
    that run; cleared succeeds immediately.
    """
    consecutive_holds = 0
    for _ in range(_SUBMIT_VERIFY_POLLS):
        time.sleep(_SUBMIT_POLL_SECONDS)
        state = _composer_state(pane_id, text)
        if state is _ComposerState.CLEARED:
            return state
        if state is _ComposerState.HOLDS:
            consecutive_holds += 1
            if consecutive_holds >= _SUBMIT_STABLE_HOLDS:
                return _ComposerState.HOLDS
        else:
            consecutive_holds = 0
    return _ComposerState.UNKNOWN


def wait_for_composer_ready(
    pane_id: str,
    *,
    timeout: float,
    poll: float = 0.5,
    stable_polls: int = 3,
    stable_floor: float = STABLE_READY_FLOOR_SECONDS,
) -> bool:
    """Poll until a positively live composer and full pane are stable.

    A composer glyph is necessary but insufficient: resumed Claude can display
    a framed bare composer while auto-compaction is still changing the pane.
    Readiness requires the entire capture to remain unchanged across
    ``stable_polls`` consecutive polls and the startup ``stable_floor`` to
    elapse. A stable pane without a positively live composer never qualifies.

    Returns True once ready, False if the budget is exhausted without either
    condition. Capture failure or a missing live composer resets the stability
    streak.
    """
    start = time.monotonic()
    deadline = start + timeout
    last_text: str | None = None
    stable_count = 0
    while True:
        pane_text = _capture_pane(pane_id)
        if pane_text is not None and _idle_composer_present(pane_text):
            if pane_text == last_text:
                stable_count += 1
                if (
                    stable_count >= stable_polls
                    and time.monotonic() - start >= stable_floor
                ):
                    logger.debug("Pane %s ready (stable live composer)", pane_id)
                    return True
            else:
                stable_count = 1
                last_text = pane_text
        else:
            stable_count = 0
            last_text = None
        if time.monotonic() >= deadline:
            logger.debug("Pane %s readiness not confirmed (timeout)", pane_id)
            return False
        time.sleep(poll)


def inject_text(pane_id: str, text: str) -> bool:
    """Paste ``text`` into a tmux pane and submit it (hardened).

    Synchronous and subprocess-driven (sleeps), so daemon callers must wrap it
    in ``asyncio.to_thread``. Returns True if the send sequence completed,
    False if a tmux command failed.
    """
    try:
        if _pane_in_copy_mode(pane_id):
            subprocess.run(
                ["tmux", "send-keys", "-t", pane_id, "-X", "cancel"],
                capture_output=True,
                check=True,
            )
        subprocess.run(
            ["tmux", "send-keys", "-t", pane_id, "-l", text],
            capture_output=True,
            check=True,
        )
        time.sleep(min(0.5 + len(text) / 4000.0, 1.5))
        subprocess.run(
            ["tmux", "send-keys", "-t", pane_id, "-H", "1b", "5b", "32", "30", "31", "7e"],
            capture_output=True,
            check=True,
        )
        time.sleep(0.1)
        for attempt in range(1, _MAX_SUBMIT_ATTEMPTS + 1):
            subprocess.run(
                ["tmux", "send-keys", "-t", pane_id, "Enter"],
                capture_output=True,
                check=True,
            )
            state = _wait_for_submit_state(pane_id, text)
            if state is _ComposerState.CLEARED:
                return True
            if state is _ComposerState.UNKNOWN:
                logger.error(
                    "Pane %s submit state unknown after attempt %d; not sending "
                    "another Enter",
                    pane_id,
                    attempt,
                )
                return False
            if attempt == _MAX_SUBMIT_ATTEMPTS:
                logger.error(
                    "Pane %s composer still holds injected text after %d submit attempts",
                    pane_id,
                    attempt,
                )
                return False
            logger.warning(
                "Pane %s composer still holds injected text after submit attempt %d; "
                "retrying",
                pane_id,
                attempt,
            )
        return False
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        logger.error(f"Failed to send keys to {pane_id}: {e}")
        return False
