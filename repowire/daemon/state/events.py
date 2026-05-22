"""SQLite-backed dashboard event persistence."""

from __future__ import annotations

import json
import logging
from pathlib import Path
from typing import Any

from repowire.daemon.state.database import StateDatabase

logger = logging.getLogger(__name__)


class SQLiteEventStore:
    """EventLog persistence adapter backed by the daemon state DB."""

    def __init__(self, db: StateDatabase, legacy_path: Path | None = None) -> None:
        self._db = db
        self._conn = db.conn
        if legacy_path is not None:
            self._import_legacy_if_empty(legacy_path)

    def _import_legacy_if_empty(self, legacy_path: Path) -> None:
        already_recorded = self._conn.execute(
            "SELECT 1 FROM legacy_imports WHERE source_path = ?",
            (str(legacy_path),),
        ).fetchone()
        if already_recorded is not None:
            return
        if not legacy_path.exists():
            return
        try:
            raw_data = json.loads(legacy_path.read_text())
            if not isinstance(raw_data, list):
                raise TypeError("top-level events JSON must be a list")
            events = [event for event in raw_data if isinstance(event, dict)]
            if len(events) != len(raw_data):
                raise TypeError("all event entries must be objects")
        except (json.JSONDecodeError, TypeError) as e:
            logger.error("Corrupt legacy events file (%s); skipping SQLite import", e)
            with self._conn:
                self._record_import(legacy_path, 0, "error", type(e).__name__)
            return
        imported = 0
        with self._conn:
            for event in events:
                if self._insert_event(event):
                    imported += 1
            self._record_import(legacy_path, imported, "ok", None)
        logger.info("Imported %d legacy events into SQLite state", imported)

    def _record_import(
        self,
        legacy_path: Path,
        row_count: int,
        status: str,
        error: str | None,
    ) -> None:
        try:
            st = legacy_path.stat()
            source_mtime = st.st_mtime
            source_size = st.st_size
        except OSError:
            source_mtime = None
            source_size = None
        self._conn.execute(
            """
            INSERT OR REPLACE INTO legacy_imports(
                source_path, source_mtime, source_size, row_count, status, error
            ) VALUES (?, ?, ?, ?, ?, ?)
            """,
            (str(legacy_path), source_mtime, source_size, row_count, status, error),
        )

    @staticmethod
    def _columns_for_event(
        event: dict[str, Any],
    ) -> tuple[
        str,
        str,
        str,
        str | None,
        str | None,
        str | None,
        str | None,
        str,
    ]:
        event_id = str(event["id"])
        event_type = str(event["type"])
        timestamp = str(event["timestamp"])
        peer_id = event.get("peer_id")
        peer_name = event.get("peer") or event.get("from_peer") or event.get("to_peer")
        session_id = event.get("session_id") or event.get("repowire_session_id")
        turn_id = event.get("turn_id")
        payload_json = json.dumps(event, separators=(",", ":"), sort_keys=True)
        return (
            event_id,
            event_type,
            timestamp,
            str(peer_id) if peer_id is not None else None,
            str(peer_name) if peer_name is not None else None,
            str(session_id) if session_id is not None else None,
            str(turn_id) if turn_id is not None else None,
            payload_json,
        )

    def _insert_event(self, event: dict[str, Any]) -> bool:
        if not all(key in event for key in ("id", "type", "timestamp")):
            logger.warning("Skipping malformed event row during SQLite import")
            return False
        self._conn.execute(
            """
            INSERT OR REPLACE INTO events(
                event_id, type, timestamp, peer_id, peer_name, session_id,
                turn_id, payload_json
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            self._columns_for_event(event),
        )
        return True

    def append(self, event: dict[str, Any]) -> None:
        with self._conn:
            self._insert_event(event)

    def update(self, event: dict[str, Any]) -> None:
        self.append(event)

    def load_recent(self, limit: int) -> list[dict[str, Any]]:
        rows = self._conn.execute(
            """
            SELECT payload_json FROM events
            ORDER BY timestamp DESC, rowid DESC
            LIMIT ?
            """,
            (limit,),
        ).fetchall()
        events: list[dict[str, Any]] = []
        for row in reversed(rows):
            try:
                event = json.loads(row["payload_json"])
            except (json.JSONDecodeError, TypeError):
                logger.warning("Skipping corrupt SQLite event payload")
                continue
            if isinstance(event, dict):
                events.append(event)
        return events

    def count(self) -> int:
        row = self._conn.execute("SELECT COUNT(*) FROM events").fetchone()
        return int(row[0]) if row is not None else 0
