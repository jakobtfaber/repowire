"""Normalize agent-specific hook payloads into a common format.

Each agent runtime (Claude Code, Codex, Gemini) sends different field names
for the same concepts. This adapter is the ONLY place that knows about
these differences. Handlers work with the normalized output.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

# Event name normalization
_EVENT_MAP = {
    "AfterAgent": "Stop",
    "StopFailure": "Stop",
    "BeforeAgent": "UserPromptSubmit",
}


@dataclass
class HookPayload:
    """Normalized hook payload, agent-agnostic."""

    event: str  # Canonical: "SessionStart", "Stop", "UserPromptSubmit"
    session_id: str
    cwd: str
    transcript_path: str | None
    response_text: str | None  # Agent's response (Stop hooks only)
    model: str | None  # Observed runtime model, when the backend hook exposes it
    backend: str
    raw: dict  # Original payload for any agent-specific needs


def extract_model(input_data: dict) -> str | None:
    """Extract an explicit runtime model from a hook payload.

    Backend gotchas:
    - Claude Code currently exposes model on SessionStart.
    - Codex exposes model as a hook input field more broadly.
    - Gemini/Antigravity are best-effort only; do not parse command strings.
    """
    value = input_data.get("model")
    if isinstance(value, str) and value:
        return value
    if isinstance(value, dict):
        for key in ("modelID", "model_id", "id", "name"):
            nested = value.get(key)
            if isinstance(nested, str) and nested:
                return nested
    return None


def normalize(input_data: dict, backend: str) -> HookPayload:
    """Normalize an agent-specific hook payload into a common format."""
    raw_event = input_data.get("hook_event_name", "")
    event = _EVENT_MAP.get(raw_event, raw_event)

    # Response text: each agent uses a different field name.
    # Use explicit None checks so empty strings aren't skipped.
    _fields = ("prompt_response", "last_assistant_message", "final_response")
    response_text = next(
        (input_data[f] for f in _fields if input_data.get(f) is not None), None,
    )

    return HookPayload(
        event=event,
        session_id=input_data.get("session_id", ""),
        cwd=input_data.get("cwd", ""),
        transcript_path=input_data.get("transcript_path"),
        response_text=response_text,
        model=extract_model(input_data),
        backend=backend,
        raw=input_data,
    )


def hook_output(backend: str) -> None:
    """Print required hook output to stdout. Gemini needs explicit approval.

    Antigravity CLI shares Gemini's hook event names (BeforeAgent/AfterAgent)
    and JSON-decision shape based on its plugin schema, so it gets the same
    explicit allow. Whether the Antigravity CLI actually fires plugin-defined
    hooks today is pending upstream verification.
    """
    if backend in ("gemini", "antigravity"):
        print(json.dumps({"decision": "allow"}))
