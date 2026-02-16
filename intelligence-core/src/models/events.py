import json
import logging
import sqlite3
import uuid
from dataclasses import dataclass, field, asdict
from datetime import datetime
from typing import Optional

log = logging.getLogger("intelligence-core.events")


@dataclass
class Event:
    source: str
    source_id: str
    event_type: str
    timestamp: datetime
    sender_name: Optional[str] = None
    sender_id: Optional[str] = None
    recipient_name: Optional[str] = None
    channel_name: Optional[str] = None
    channel_id: Optional[str] = None
    title: Optional[str] = None
    content: Optional[str] = None
    domain: Optional[str] = None
    importance: str = "normal"
    metadata: dict = field(default_factory=dict)
    id: str = field(default_factory=lambda: str(uuid.uuid4()))


class EventStore:
    def __init__(self, db_path: str):
        self.db_path = db_path
        self._init_db()

    def _init_db(self):
        with sqlite3.connect(self.db_path) as conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS events (
                    id TEXT PRIMARY KEY,
                    source TEXT NOT NULL,
                    source_id TEXT,
                    event_type TEXT NOT NULL,
                    timestamp TIMESTAMP NOT NULL,
                    sender_name TEXT,
                    sender_id TEXT,
                    recipient_name TEXT,
                    channel_name TEXT,
                    channel_id TEXT,
                    title TEXT,
                    content TEXT,
                    domain TEXT,
                    importance TEXT DEFAULT 'normal',
                    metadata TEXT,
                    collected_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    UNIQUE(source, source_id)
                )
            """)
            conn.execute("CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp)")
            conn.execute("CREATE INDEX IF NOT EXISTS idx_events_source ON events(source)")
            conn.execute("CREATE INDEX IF NOT EXISTS idx_events_domain ON events(domain)")

            conn.execute("""
                CREATE TABLE IF NOT EXISTS collection_log (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    source TEXT NOT NULL,
                    collected_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    events_collected INTEGER,
                    status TEXT,
                    error_message TEXT,
                    duration_seconds REAL
                )
            """)

    def store_events(self, events: list[Event]) -> int:
        stored = 0
        with sqlite3.connect(self.db_path) as conn:
            for event in events:
                try:
                    conn.execute(
                        """INSERT OR IGNORE INTO events
                        (id, source, source_id, event_type, timestamp,
                         sender_name, sender_id, recipient_name,
                         channel_name, channel_id, title, content,
                         domain, importance, metadata)
                        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                        (
                            event.id, event.source, event.source_id,
                            event.event_type, event.timestamp.isoformat(),
                            event.sender_name, event.sender_id,
                            event.recipient_name, event.channel_name,
                            event.channel_id, event.title, event.content,
                            event.domain, event.importance,
                            json.dumps(event.metadata) if event.metadata else None,
                        ),
                    )
                    if conn.total_changes:
                        stored += 1
                except sqlite3.IntegrityError:
                    pass
        return stored

    def get_events_since(self, since: datetime, source: Optional[str] = None) -> list[Event]:
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            if source:
                rows = conn.execute(
                    "SELECT * FROM events WHERE timestamp > ? AND source = ? ORDER BY timestamp",
                    (since.isoformat(), source),
                ).fetchall()
            else:
                rows = conn.execute(
                    "SELECT * FROM events WHERE timestamp > ? ORDER BY timestamp",
                    (since.isoformat(),),
                ).fetchall()
        return [self._row_to_event(r) for r in rows]

    def log_collection(self, source: str, count: int, status: str,
                       error: Optional[str] = None, duration: float = 0.0):
        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                """INSERT INTO collection_log
                (source, events_collected, status, error_message, duration_seconds)
                VALUES (?, ?, ?, ?, ?)""",
                (source, count, status, error, duration),
            )

    def _row_to_event(self, row: sqlite3.Row) -> Event:
        meta = {}
        if row["metadata"]:
            try:
                meta = json.loads(row["metadata"])
            except json.JSONDecodeError:
                pass
        return Event(
            id=row["id"],
            source=row["source"],
            source_id=row["source_id"],
            event_type=row["event_type"],
            timestamp=datetime.fromisoformat(row["timestamp"]),
            sender_name=row["sender_name"],
            sender_id=row["sender_id"],
            recipient_name=row["recipient_name"],
            channel_name=row["channel_name"],
            channel_id=row["channel_id"],
            title=row["title"],
            content=row["content"],
            domain=row["domain"],
            importance=row["importance"],
            metadata=meta,
        )
