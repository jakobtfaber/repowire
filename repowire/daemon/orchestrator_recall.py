"""Daemon-side inbound recall triage for orchestrator peers."""

from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from repowire.config.models import Config, OrchestratorRecallConfig
from repowire.orchestrator.workspace import workspace_path
from repowire.protocol.peers import PeerRole

_TOKEN_RE = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.@/-]{2,}")
_RECALL_OPEN = "[repowire recall]"
_RECALL_CLOSE = "[/repowire recall]"


@dataclass(frozen=True)
class RecallSource:
    label: str
    path: Path


@dataclass(frozen=True)
class RecallHit:
    label: str
    score: int
    snippet: str


def maybe_add_orchestrator_recall(
    text: str,
    *,
    config: Config,
    target: Any,
    from_peer_name: str,
    from_peer_id: str | None = None,
) -> str:
    """Prefix relevant orchestrator memory for inbound messages, when enabled."""
    settings = config.daemon.orchestrator_recall
    if not settings.enabled:
        return text
    if getattr(target, "role", None) != PeerRole.ORCHESTRATOR:
        return text

    query = " ".join(
        value
        for value in (
            text,
            from_peer_name,
            from_peer_id or "",
            getattr(target, "display_name", ""),
            getattr(target, "peer_id", ""),
            getattr(target, "circle", ""),
        )
        if value
    )
    hits = retrieve_orchestrator_recall(query, settings=settings)
    if not hits:
        return text
    return f"{format_recall_block(hits, settings=settings)}\n\n{text}"


def retrieve_orchestrator_recall(
    query: str,
    *,
    settings: OrchestratorRecallConfig,
) -> list[RecallHit]:
    """Return top lexical recall hits for the query from orchestrator memory files."""
    tokens = _tokens(query)
    if not tokens:
        return []

    hits: list[RecallHit] = []
    for source in _recall_sources():
        try:
            content = source.path.read_text(encoding="utf-8")[: settings.max_file_chars]
        except (OSError, UnicodeDecodeError):
            continue
        score = _score(content, source.label, tokens)
        if score <= 0:
            continue
        snippet = _best_snippet(content, tokens)
        if snippet:
            hits.append(RecallHit(label=source.label, score=score, snippet=snippet))

    hits.sort(key=lambda hit: (-hit.score, hit.label))
    return hits[: settings.max_hits]


def format_recall_block(
    hits: list[RecallHit],
    *,
    settings: OrchestratorRecallConfig,
) -> str:
    """Render hits into a compact injected context block."""
    lines = [_RECALL_OPEN]
    for hit in hits:
        lines.append(f"- {hit.label}: {hit.snippet}")
    lines.append(_RECALL_CLOSE)
    block = "\n".join(lines)
    if len(block) <= settings.max_chars:
        return block
    budget = max(settings.max_chars - len(_RECALL_OPEN) - len(_RECALL_CLOSE) - 8, 20)
    rendered: list[str] = [_RECALL_OPEN]
    for hit in hits:
        prefix = f"- {hit.label}: "
        remaining = budget - sum(len(line) + 1 for line in rendered) - len(_RECALL_CLOSE)
        if remaining <= len(prefix) + 12:
            break
        rendered.append(prefix + _truncate(hit.snippet, remaining - len(prefix)))
    rendered.append(_RECALL_CLOSE)
    return "\n".join(rendered)


def _recall_sources() -> list[RecallSource]:
    root = workspace_path()
    sources = [
        RecallSource("comms.md", root / "comms.md"),
        RecallSource("projects.md", root / "projects.md"),
    ]
    memory_dir = root / "memory"
    if memory_dir.is_dir():
        for path in sorted(memory_dir.glob("*.md"), key=lambda p: p.name):
            if path.name == "MEMORY.md":
                continue
            sources.append(RecallSource(f"memory/{path.name}", path))
    return sources


def _tokens(text: str) -> set[str]:
    stop = {
        "from",
        "with",
        "that",
        "this",
        "there",
        "reply",
        "ack",
        "ask",
        "message",
        "repowire",
    }
    return {
        token.strip("._-/@").lower()
        for token in _TOKEN_RE.findall(text)
        if token.strip("._-/@").lower() not in stop
    }


def _score(content: str, label: str, query_tokens: set[str]) -> int:
    haystack = f"{label}\n{content}".lower()
    score = 0
    for token in query_tokens:
        count = haystack.count(token)
        if count:
            score += min(count, 4)
            if token in label.lower():
                score += 2
    return score


def _best_snippet(content: str, query_tokens: set[str]) -> str:
    candidates = []
    for line in content.splitlines():
        clean = line.strip()
        if not clean or clean in {"---"}:
            continue
        lowered = clean.lower()
        score = sum(1 for token in query_tokens if token in lowered)
        if score:
            candidates.append((score, clean))
    if not candidates:
        return ""
    candidates.sort(key=lambda item: (-item[0], len(item[1])))
    return _truncate(candidates[0][1], 220)


def _truncate(text: str, limit: int) -> str:
    if len(text) <= limit:
        return text
    if limit <= 1:
        return text[:limit]
    return text[: limit - 1].rstrip() + "…"
