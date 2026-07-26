"""Tests for the shared hardened tmux paste injector (repowire.tmux_inject).

These cover the bracketed-paste + Enter-swallow-retry sequence (migrated from
the old websocket_hook TestTmuxSendKeys) and the readiness-polling helper used
by the daemon's post-spawn seed.
"""

from __future__ import annotations

from subprocess import CompletedProcess
from unittest.mock import patch

from repowire.tmux_inject import inject_text, wait_for_composer_ready


def _mode_result(in_mode: bool) -> CompletedProcess:
    return CompletedProcess(
        args=[], returncode=0, stdout=("1" if in_mode else "0") + "\n", stderr=""
    )


def _ok() -> CompletedProcess:
    return CompletedProcess(args=[], returncode=0, stdout="", stderr="")


def _capture(text: str) -> CompletedProcess:
    return CompletedProcess(args=[], returncode=0, stdout=text, stderr="")


def _held_through_submit_grace(text: str) -> list[CompletedProcess]:
    """Captures that hold the prompt throughout the 1.2-second grace window."""
    return [_capture(text) for _ in range(6)]


def _through_submit_observation(result: CompletedProcess) -> list[CompletedProcess]:
    """Repeat one observation result across the bounded eight-poll window."""
    return [result for _ in range(8)]


def _fake_clock():
    """A monotonic/sleep pair backed by a shared mutable clock so elapsed time
    advances deterministically by the slept amount on each sleep."""
    now = {"t": 0.0}

    def monotonic() -> float:
        return now["t"]

    def sleep(seconds: float) -> None:
        now["t"] += seconds

    return monotonic, sleep


class TestInjectText:
    """inject_text drives the hardened paste/submit sequence."""

    def test_closes_bracketed_paste_without_bare_escape(self):
        """Normal mode: no -X cancel, just literal/paste-close/Enter sequence."""
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),  # display-message (copy-mode probe)
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),  # Enter
                _capture("❯ \n"),  # capture-pane: empty composer (submitted)
            ]
            assert inject_text("%5", "hello") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls == [
            ["tmux", "display-message", "-t", "%5", "-p", "#{pane_in_mode}"],
            ["tmux", "send-keys", "-t", "%5", "-l", "hello"],
            ["tmux", "send-keys", "-t", "%5", "-H", "1b", "5b", "32", "30", "31", "7e"],
            ["tmux", "send-keys", "-t", "%5", "Enter"],
            ["tmux", "capture-pane", "-t", "%5", "-p", "-J", "-S", "-30"],
        ]
        assert ["tmux", "send-keys", "-t", "%5", "Escape"] not in calls
        assert ["tmux", "send-keys", "-t", "%5", "-X", "cancel"] not in calls

    def test_cancels_copy_mode_before_paste(self):
        """Copy-mode: -X cancel runs before the literal paste, in that order."""
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(True),  # display-message reports copy-mode
                _ok(),  # -X cancel
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),  # Enter
                _capture("❯ \n"),  # empty composer (submitted)
            ]
            assert inject_text("%5", "hello") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls == [
            ["tmux", "display-message", "-t", "%5", "-p", "#{pane_in_mode}"],
            ["tmux", "send-keys", "-t", "%5", "-X", "cancel"],
            ["tmux", "send-keys", "-t", "%5", "-l", "hello"],
            ["tmux", "send-keys", "-t", "%5", "-H", "1b", "5b", "32", "30", "31", "7e"],
            ["tmux", "send-keys", "-t", "%5", "Enter"],
            ["tmux", "capture-pane", "-t", "%5", "-p", "-J", "-S", "-30"],
        ]
        cancel_idx = calls.index(["tmux", "send-keys", "-t", "%5", "-X", "cancel"])
        paste_idx = calls.index(["tmux", "send-keys", "-t", "%5", "-l", "hello"])
        assert cancel_idx < paste_idx, "cancel must precede the literal paste"

    def test_mode_probe_failure_treated_as_not_in_mode(self):
        """If display-message fails, skip cancel rather than blocking the send."""
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                CompletedProcess(args=[], returncode=1, stdout="", stderr="no pane"),
                _ok(),
                _ok(),
                _ok(),
                _capture("❯ \n"),
            ]
            assert inject_text("%5", "hello") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert ["tmux", "send-keys", "-t", "%5", "-X", "cancel"] not in calls

    def test_swallowed_enter_is_resent_once(self):
        """If the composer still holds the injected text after Enter (paste
        heuristic swallowed it as a newline), nudge with exactly one more
        Enter — but only on positive evidence."""
        composer_stuck = "❯ old prompt history\n✶ thinking\n❯ hello there friend\n"
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),  # Enter
                *_held_through_submit_grace(composer_stuck),
                _ok(),  # retry Enter
                _capture("❯ \n"),  # retry submitted; composer now empty
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 2

    def test_initial_unknown_then_stable_holds_retries_submit(self):
        composer_stuck = "❯ hello there friend\n"
        capture_failed = CompletedProcess(
            args=[], returncode=1, stdout="", stderr="transient capture failure"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),  # first Enter swallowed
                capture_failed,  # transient UNKNOWN
                *_held_through_submit_grace(composer_stuck),
                _ok(),  # retry Enter submits
                _capture("❯ \n"),
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 2

    def test_two_initial_unknowns_then_six_holds_retry_on_last_poll(self):
        composer_stuck = "❯ hello there friend\n"
        capture_failed = CompletedProcess(
            args=[], returncode=1, stdout="", stderr="transient capture failure"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                capture_failed,
                capture_failed,
                *_held_through_submit_grace(composer_stuck),
                _ok(),
                _capture("❯ \n"),
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 2

    def test_intermittent_unknown_resets_holds_and_does_not_retry(self):
        composer_stuck = "❯ hello there friend\n"
        capture_failed = CompletedProcess(
            args=[], returncode=1, stdout="", stderr="transient capture failure"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *[_capture(composer_stuck) for _ in range(5)],
                capture_failed,
                _capture(composer_stuck),
                _capture(composer_stuck),
            ]
            assert inject_text("%5", "hello there friend") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_two_swallowed_enters_are_retried_until_composer_is_empty(self):
        """Claude can swallow both the initial Enter and first retry.

        Keep submitting only while each capture positively shows the injected
        text in the live composer, then stop as soon as it is empty.
        """
        composer_stuck = "❯ hello there friend\n"
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),  # first Enter swallowed
                *_held_through_submit_grace(composer_stuck),
                _ok(),  # second Enter swallowed
                *_held_through_submit_grace(composer_stuck),
                _ok(),  # third Enter submits
                _capture("❯ \n"),
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 3

    def test_multiple_stale_held_frames_then_delayed_clear_do_not_retry(self):
        composer_stuck = "❯ hello there friend\n"
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),  # Enter submits
                *[_capture(composer_stuck) for _ in range(5)],
                _capture("❯ \n"),  # delayed clear
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_clear_after_holds_and_unknown_returns_without_retry(self):
        composer_stuck = "❯ hello there friend\n"
        capture_failed = CompletedProcess(
            args=[], returncode=1, stdout="", stderr="transient capture failure"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *[_capture(composer_stuck) for _ in range(3)],
                capture_failed,
                _capture(composer_stuck),
                _capture(composer_stuck),
                _capture("❯ \n"),
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_submit_retries_stop_at_bound_when_composer_stays_full(self):
        composer_stuck = "❯ hello there friend\n"
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),
                *_held_through_submit_grace(composer_stuck),
                _ok(),
                *_held_through_submit_grace(composer_stuck),
                _ok(),
                *_held_through_submit_grace(composer_stuck),
            ]
            assert inject_text("%5", "hello there friend") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 3

    def test_final_attempt_unknown_fails_without_extra_enter(self):
        composer_stuck = "❯ hello there friend\n"
        capture_failed = CompletedProcess(
            args=[], returncode=1, stdout="", stderr="pane unavailable"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),  # -l text
                _ok(),  # -H close
                _ok(),
                *_held_through_submit_grace(composer_stuck),
                _ok(),
                *_held_through_submit_grace(composer_stuck),
                _ok(),
                *_through_submit_observation(capture_failed),
            ]
            assert inject_text("%5", "hello there friend") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 3

    def test_joined_capture_detects_held_long_wrapped_composer(self):
        text = "submit this long seed prompt " + " ".join(["word"] * 40)
        composer_stuck = (
            "────────────────────────────────────────\n"
            "❯\xa0submit this long seed\n"
            "  prompt " + " ".join(["word"] * 20) + "\n"
            "  " + " ".join(["word"] * 20) + "\n"
            "────────────────────────────────────────\n"
            "  bypass permissions on\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *_held_through_submit_grace(composer_stuck),
                _ok(),
                _capture("❯ \n"),
            ]
            assert inject_text("%5", text) is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 2
        assert ["tmux", "capture-pane", "-t", "%5", "-p", "-J", "-S", "-30"] in calls

    def test_different_nonempty_composer_is_unknown_not_cleared(self):
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *_through_submit_observation(_capture("❯ different text\n")),
            ]
            assert inject_text("%5", "hello there friend") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_submitted_prompt_followed_by_processing_is_not_live_composer(self):
        processing = (
            "❯ hello there friend\n"
            "⏺ Reading the repository\n"
            "  Working…\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *_through_submit_observation(_capture(processing)),
            ]
            assert inject_text("%5", "hello there friend") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_submitted_prompt_plus_output_before_divider_is_not_live_composer(self):
        processing = (
            "❯ hello there friend\n"
            "\n"
            "  Called repowire 1 time\n"
            "────────────────────────────────────────\n"
            "  bypass permissions on\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *_through_submit_observation(_capture(processing)),
            ]
            assert inject_text("%5", "hello there friend") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_partial_long_composer_content_is_unknown(self):
        text = "submit this long seed prompt " + "x" * 200
        partial = (
            "────────────────────────────────────────\n"
            f"❯ {text[:70]}\n"
            "────────────────────────────────────────\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *_through_submit_observation(_capture(partial)),
            ]
            assert inject_text("%5", text) is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_submitted_text_in_transcript_does_not_trigger_resend(self):
        """A submitted prompt stays visible in the transcript; only text in
        the bottom-most composer prompt counts as unsubmitted."""
        submitted = "❯ hello there friend\n⏺ on it\n❯ \n"
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                _capture(submitted),
            ]
            assert inject_text("%5", "hello there friend") is True

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_capture_failure_does_not_resend(self):
        """No retry on uncertainty: a failed capture must not nudge."""
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                _ok(),
                _ok(),
                _ok(),
                *_through_submit_observation(
                    CompletedProcess(args=[], returncode=1, stdout="", stderr="boom")
                ),
            ]
            assert inject_text("%5", "hello") is False

        calls = [call.args[0] for call in mock_run.call_args_list]
        assert calls.count(["tmux", "send-keys", "-t", "%5", "Enter"]) == 1

    def test_send_failure_returns_false(self):
        """A failing tmux send-keys surfaces as False, not a swallowed success."""
        from subprocess import CalledProcessError

        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep"),
        ):
            mock_run.side_effect = [
                _mode_result(False),
                CalledProcessError(1, ["tmux"]),  # -l text fails
            ]
            assert inject_text("%5", "hello") is False


class TestWaitForComposerReady:
    """wait_for_composer_ready polls capture-pane for the composer prompt."""

    def test_changing_compaction_frames_with_bare_composer_are_not_ready(self):
        monotonic, sleep = _fake_clock()

        def compaction_frame(step: int) -> str:
            return (
                f"Compacting conversation… step {step}\n"
                "────────────────────────────────────────\n"
                "❯ \n"
                "────────────────────────────────────────\n"
            )

        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.side_effect = [
                _capture(compaction_frame(step)) for step in range(4)
            ]
            assert (
                wait_for_composer_ready(
                    "%5", timeout=3.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_stable_busy_compaction_with_bare_composer_is_not_ready(self):
        monotonic, sleep = _fake_clock()
        busy_compaction = (
            "Compacting conversation… 64%\n"
            "────────────────────────────────────────\n"
            "❯ \n"
            "────────────────────────────────────────\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture(busy_compaction)
            assert (
                wait_for_composer_ready(
                    "%5", timeout=6.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_stable_ascii_compaction_status_is_not_ready(self):
        monotonic, sleep = _fake_clock()
        busy_compaction = (
            "✽ Compacting conversation...\n"
            "────────────────────────────────────────\n"
            "❯ \n"
            "────────────────────────────────────────\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture(busy_compaction)
            assert (
                wait_for_composer_ready(
                    "%5", timeout=6.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_stable_claude_progress_bar_is_not_ready(self):
        monotonic, sleep = _fake_clock()
        busy_progress = (
            "Compaction in progress\n"
            "▰▰▰▱▱ 60%\n"
            "────────────────────────────────────────\n"
            "❯ \n"
            "────────────────────────────────────────\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture(busy_progress)
            assert (
                wait_for_composer_ready(
                    "%5", timeout=6.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_stable_idle_footer_with_truncated_path_is_ready_after_floor(self):
        monotonic, sleep = _fake_clock()
        idle = (
            "✻ Crunched for 5s\n"
            "────────────────────────────────────────\n"
            "❯ \n"
            "────────────────────────────────────────\n"
            "  py312 user:~/work/.repowire-acceptance…\n"
            "  bypass permissions on\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture(idle)
            assert (
                wait_for_composer_ready(
                    "%5", timeout=10.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is True
            )

    def test_stable_nonempty_live_composer_is_not_ready(self):
        monotonic, sleep = _fake_clock()
        nonempty = (
            "────────────────────────────────────────\n"
            "❯ unsent user text\n"
            "────────────────────────────────────────\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture(nonempty)
            assert (
                wait_for_composer_ready(
                    "%5", timeout=6.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_historical_prompt_does_not_signal_resumed_composer_ready(self):
        monotonic, sleep = _fake_clock()
        historical = "❯ old submitted prompt\n⏺ old completed response\n"
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture(historical)
            assert (
                wait_for_composer_ready(
                    "%5", timeout=3.0, poll=1.0, stable_polls=2, stable_floor=0.0
                )
                is False
            )

    def test_stable_idle_composer_is_ready_only_after_floor(self):
        monotonic, sleep = _fake_clock()
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep) as mock_sleep,
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture("❯ \n")
            assert (
                wait_for_composer_ready(
                    "%5", timeout=10.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is True
            )
            assert mock_sleep.call_count == 5

    def test_live_composer_builds_fresh_streak_after_stable_nonprompt_frames(self):
        monotonic, sleep = _fake_clock()
        live_composer = (
            "────────────────────────────────────────\n"
            "❯ \n"
            "────────────────────────────────────────\n"
        )
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep) as mock_sleep,
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.side_effect = [
                _capture("booting...\n"),
                _capture("booting...\n"),
                _capture("booting...\n"),
                _capture(live_composer),
                _capture(live_composer),
                _capture(live_composer),
            ]
            assert (
                wait_for_composer_ready(
                    "%5", timeout=10.0, poll=1.0, stable_polls=3, stable_floor=0.0
                )
                is True
            )
            assert mock_sleep.call_count == 5

    def test_returns_false_on_timeout(self):
        monotonic, sleep = _fake_clock()
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture("never shows a prompt\n")
            # Changing/non-glyph content never matches; clock advances past the
            # deadline via the sleeps, so the wait times out. (poll=5 reaches
            # the 5s deadline in two iterations.)
            assert wait_for_composer_ready("%5", timeout=5.0, poll=5.0) is False

    def test_capture_failure_is_not_ready_keeps_polling(self):
        monotonic, sleep = _fake_clock()
        live_composer = _capture("❯ \n")
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.side_effect = [
                live_composer,
                live_composer,
                CompletedProcess(args=[], returncode=1, stdout="", stderr="boom"),
                live_composer,
                live_composer,
                live_composer,
            ]
            assert (
                wait_for_composer_ready(
                    "%5", timeout=10.0, poll=1.0, stable_polls=3, stable_floor=0.0
                )
                is True
            )

    def test_stable_content_without_live_composer_is_not_ready(self):
        monotonic, sleep = _fake_clock()
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture("weird-prompt$ \n")
            assert (
                wait_for_composer_ready(
                    "%5", timeout=6.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_stable_does_not_fire_before_floor(self):
        """A stable streak reached before stable_floor must NOT declare ready —
        firing at ~1s would be worse than main's blind 5s sleep. With a short
        timeout the call returns False (caller then injects as fallback)."""
        monotonic, sleep = _fake_clock()
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.return_value = _capture("❯ \n")
            assert (
                wait_for_composer_ready(
                    "%5", timeout=3.0, poll=1.0, stable_polls=2, stable_floor=5.0
                )
                is False
            )

    def test_changing_content_resets_stability_streak(self):
        """Content that keeps changing never trips the stable fallback; falls
        through to timeout (then the caller injects anyway)."""
        monotonic, sleep = _fake_clock()
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.side_effect = [
                _capture("line 1\n"),
                _capture("line 1\nline 2\n"),
                _capture("line 1\nline 2\nline 3\n"),
                _capture("line 1\nline 2\nline 3\nline 4\n"),
            ]
            assert (
                wait_for_composer_ready(
                    "%5", timeout=3.0, poll=1.0, stable_polls=3, stable_floor=5.0
                )
                is False
            )

    def test_empty_capture_does_not_count_as_stable(self):
        """Blank/whitespace-only captures reset the streak — an empty pane is
        not 'ready', it's just not booted yet."""
        monotonic, sleep = _fake_clock()
        with (
            patch("repowire.tmux_inject.subprocess.run") as mock_run,
            patch("repowire.tmux_inject.time.sleep", side_effect=sleep),
            patch("repowire.tmux_inject.time.monotonic", side_effect=monotonic),
        ):
            mock_run.side_effect = [
                _capture("\n"),
                _capture("   \n"),
                _capture("\n"),
                _capture("❯ \n"),
                _capture("❯ \n"),
                _capture("❯ \n"),
            ]
            assert (
                wait_for_composer_ready(
                    "%5", timeout=10.0, poll=1.0, stable_polls=3, stable_floor=0.0
                )
                is True
            )
