"""SQLite-backed recurring durable job calendar entries."""

from __future__ import annotations

import sqlite3
import threading
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any
from uuid import uuid4

from repowire.daemon.schedule_cron import next_fire_after, validate_cron
from repowire.daemon.state.database import StateDatabase
from repowire.daemon.work_store import TrackedWork, json_dumps, json_loads, now_iso


def new_calendar_id() -> str:
    return f"cal-{uuid4().hex[:12]}"


def _parse_iso(value: str) -> datetime:
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


@dataclass(frozen=True)
class CalendarEntry:
    """Recurring durable job template that materializes tracked work occurrences."""

    calendar_id: str
    title: str
    kind: str
    state: str
    cron: str
    next_due_at: str
    visibility: str = "circle"
    owner_peer_id: str | None = None
    assigned_peer_id: str | None = None
    circle: str | None = None
    created_by_peer_id: str | None = None
    source_kind: str | None = None
    source_id: str | None = None
    scope: str | None = None
    request: dict[str, Any] = field(default_factory=dict)
    provenance: dict[str, Any] = field(default_factory=dict)
    last_occurrence_work_id: str | None = None
    last_materialized_at: str | None = None
    created_at: str = ""
    updated_at: str = ""

    def status(self) -> dict[str, Any]:
        return {
            "calendar_id": self.calendar_id,
            "recurring_id": self.calendar_id,
            "title": self.title,
            "kind": self.kind,
            "state": self.state,
            "cron": self.cron,
            "next_due_at": self.next_due_at,
            "owner_peer_id": self.owner_peer_id,
            "assigned_peer_id": self.assigned_peer_id,
            "circle": self.circle,
            "created_by_peer_id": self.created_by_peer_id,
            "source_kind": self.source_kind,
            "source_id": self.source_id,
            "scope": self.scope,
            "visibility": self.visibility,
            "request": self.request,
            "provenance": self.provenance,
            "execution": self.request.get("execution", {}),
            "last_occurrence_work_id": self.last_occurrence_work_id,
            "last_materialized_at": self.last_materialized_at,
            "created_at": self.created_at,
            "updated_at": self.updated_at,
        }


class SQLiteCalendarStore:
    """Recurring durable job templates backed by the daemon state DB."""

    def __init__(self, db: StateDatabase, work_store: Any) -> None:
        self._conn = db.conn
        self._work_store = work_store
        self._lock = threading.Lock()

    @staticmethod
    def _row_to_entry(row: sqlite3.Row | None) -> CalendarEntry | None:
        if row is None:
            return None
        return CalendarEntry(
            calendar_id=row["calendar_id"],
            title=row["title"],
            kind=row["kind"],
            state=row["state"],
            cron=row["cron"],
            next_due_at=row["next_due_at"],
            owner_peer_id=row["owner_peer_id"],
            assigned_peer_id=row["assigned_peer_id"],
            circle=row["circle"],
            created_by_peer_id=row["created_by_peer_id"],
            source_kind=row["source_kind"],
            source_id=row["source_id"],
            scope=row["scope"],
            visibility=row["visibility"],
            request=json_loads(row["request_json"], {}),
            provenance=json_loads(row["provenance_json"], {}),
            last_occurrence_work_id=row["last_occurrence_work_id"],
            last_materialized_at=row["last_materialized_at"],
            created_at=row["created_at"],
            updated_at=row["updated_at"],
        )

    def create(
        self,
        *,
        title: str,
        kind: str,
        cron: str,
        created_by_peer_id: str | None = None,
        owner_peer_id: str | None = None,
        assigned_peer_id: str | None = None,
        circle: str | None = None,
        source_kind: str | None = None,
        source_id: str | None = None,
        scope: str | None = None,
        visibility: str = "circle",
        request: dict[str, Any] | None = None,
        provenance: dict[str, Any] | None = None,
        now: datetime | None = None,
    ) -> CalendarEntry:
        normalized_cron = validate_cron(cron)
        base = now or datetime.now(timezone.utc)
        created_at = now_iso()
        entry = CalendarEntry(
            calendar_id=new_calendar_id(),
            title=title,
            kind=kind,
            state="active",
            cron=normalized_cron,
            next_due_at=next_fire_after(normalized_cron, base).isoformat(),
            owner_peer_id=owner_peer_id,
            assigned_peer_id=assigned_peer_id,
            circle=circle,
            created_by_peer_id=created_by_peer_id,
            source_kind=source_kind,
            source_id=source_id,
            scope=scope,
            visibility=visibility,
            request=request or {},
            provenance=provenance or {},
            created_at=created_at,
            updated_at=created_at,
        )
        with self._conn:
            self._conn.execute(
                """
                INSERT INTO calendar_entries(
                    calendar_id, title, kind, state, cron, next_due_at,
                    owner_peer_id, assigned_peer_id, circle, created_by_peer_id,
                    source_kind, source_id, scope, visibility, request_json,
                    provenance_json, last_occurrence_work_id, last_materialized_at,
                    created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    entry.calendar_id,
                    entry.title,
                    entry.kind,
                    entry.state,
                    entry.cron,
                    entry.next_due_at,
                    entry.owner_peer_id,
                    entry.assigned_peer_id,
                    entry.circle,
                    entry.created_by_peer_id,
                    entry.source_kind,
                    entry.source_id,
                    entry.scope,
                    entry.visibility,
                    json_dumps(entry.request),
                    json_dumps(entry.provenance),
                    entry.last_occurrence_work_id,
                    entry.last_materialized_at,
                    entry.created_at,
                    entry.updated_at,
                ),
            )
        return entry

    def get(self, calendar_id: str) -> CalendarEntry | None:
        row = self._conn.execute(
            "SELECT * FROM calendar_entries WHERE calendar_id = ?",
            (calendar_id,),
        ).fetchone()
        return self._row_to_entry(row)

    def list_all(
        self,
        *,
        state: str | None = None,
        owner_peer_id: str | None = None,
        created_by_peer_id: str | None = None,
        circle: str | None = None,
    ) -> list[CalendarEntry]:
        clauses: list[str] = []
        params: list[str] = []
        if state is not None:
            clauses.append("state = ?")
            params.append(state)
        if owner_peer_id is not None:
            clauses.append("owner_peer_id = ?")
            params.append(owner_peer_id)
        if created_by_peer_id is not None:
            clauses.append("created_by_peer_id = ?")
            params.append(created_by_peer_id)
        if circle is not None:
            clauses.append("circle = ?")
            params.append(circle)
        where = f"WHERE {' AND '.join(clauses)}" if clauses else ""
        rows = self._conn.execute(
            f"SELECT * FROM calendar_entries {where} ORDER BY next_due_at",
            params,
        ).fetchall()
        return [entry for row in rows if (entry := self._row_to_entry(row)) is not None]

    def cancel(
        self,
        calendar_id: str,
        *,
        reason: str = "cancel_requested",
    ) -> CalendarEntry | None:
        existing = self.get(calendar_id)
        if existing is None:
            return None
        provenance = dict(existing.provenance)
        provenance["cancel_reason"] = reason
        now = now_iso()
        with self._conn:
            self._conn.execute(
                """
                UPDATE calendar_entries
                SET state = 'cancelled', provenance_json = ?, updated_at = ?
                WHERE calendar_id = ?
                """,
                (json_dumps(provenance), now, calendar_id),
            )
        return self.get(calendar_id)

    def seconds_until_next_due(self, now: datetime | None = None) -> float | None:
        row = self._conn.execute(
            """
            SELECT next_due_at FROM calendar_entries
            WHERE state = 'active'
            ORDER BY next_due_at
            LIMIT 1
            """
        ).fetchone()
        if row is None:
            return None
        current = now or datetime.now(timezone.utc)
        return max(0.0, (_parse_iso(row["next_due_at"]) - current).total_seconds())

    def materialize_due(self, *, now: datetime | None = None) -> list[TrackedWork]:
        current = now or datetime.now(timezone.utc)
        materialized: list[TrackedWork] = []
        with self._lock:
            due_entries = self.list_all(state="active")
            for entry in due_entries:
                due = _parse_iso(entry.next_due_at)
                if due > current:
                    continue
                refreshed = self.get(entry.calendar_id)
                if refreshed is None or refreshed.state != "active":
                    continue
                if _parse_iso(refreshed.next_due_at) > current:
                    continue
                work = self._create_occurrence(refreshed, due_at=refreshed.next_due_at)
                next_due = next_fire_after(refreshed.cron, current)
                now_text = now_iso()
                provenance = dict(refreshed.provenance)
                provenance["last_materialized_reason"] = "due"
                with self._conn:
                    self._conn.execute(
                        """
                        UPDATE calendar_entries
                        SET next_due_at = ?, last_occurrence_work_id = ?,
                            last_materialized_at = ?, provenance_json = ?, updated_at = ?
                        WHERE calendar_id = ? AND next_due_at = ?
                        """,
                        (
                            next_due.isoformat(),
                            work.work_id,
                            now_text,
                            json_dumps(provenance),
                            now_text,
                            refreshed.calendar_id,
                            refreshed.next_due_at,
                        ),
                    )
                materialized.append(work)
        return materialized

    def _create_occurrence(self, entry: CalendarEntry, *, due_at: str) -> TrackedWork:
        request = dict(entry.request)
        execution = dict(request.get("execution") or {})
        schedule = dict(execution.get("schedule") or {})
        schedule["due_at"] = due_at
        schedule["calendar_id"] = entry.calendar_id
        schedule["cron"] = entry.cron
        execution["schedule"] = schedule
        request["execution"] = execution
        provenance = {
            "calendar": {
                "calendar_id": entry.calendar_id,
                "cron": entry.cron,
                "scheduled_due_at": due_at,
            }
        }
        return self._work_store.create(
            title=entry.title,
            kind=entry.kind,
            created_by_peer_id=entry.created_by_peer_id,
            owner_peer_id=entry.owner_peer_id,
            assigned_peer_id=entry.assigned_peer_id,
            circle=entry.circle,
            source_kind="calendar",
            source_id=entry.calendar_id,
            scope=entry.scope,
            visibility=entry.visibility,
            request=request,
            provenance=provenance,
        )
