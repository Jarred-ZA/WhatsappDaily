import logging
import sqlite3
from datetime import datetime, timedelta, timezone
from typing import Optional

from .base import Collector
from ..models.events import Event

log = logging.getLogger("intelligence-core.collectors.whatsapp")


class WhatsAppCollector(Collector):
    name = "whatsapp"

    def __init__(self, db_path: str, state_file: str):
        super().__init__(state_file)
        self.db_path = db_path

    def collect(self, since: datetime) -> list[Event]:
        try:
            conn = sqlite3.connect(f"file:{self.db_path}?mode=ro", uri=True)
            conn.row_factory = sqlite3.Row
        except sqlite3.OperationalError:
            log.warning("WhatsApp messages.db not found at %s", self.db_path)
            return []

        try:
            rows = conn.execute(
                """SELECT m.id, m.chat_jid, m.sender, m.content, m.timestamp,
                          m.is_from_me, m.media_type, m.transcription,
                          c.name as chat_name
                   FROM messages m
                   LEFT JOIN chats c ON m.chat_jid = c.jid
                   WHERE m.timestamp > ?
                   ORDER BY m.timestamp""",
                (since.isoformat(),),
            ).fetchall()
        except sqlite3.OperationalError as e:
            log.error("Failed to query messages: %s", e)
            conn.close()
            return []

        events = []
        for row in rows:
            content = row["transcription"] or row["content"] or ""
            if not content and row["media_type"]:
                content = f"[{row['media_type']}]"
            if not content:
                continue

            sender = "Jarred" if row["is_from_me"] else (row["sender"] or "Unknown")
            chat_name = row["chat_name"] or row["chat_jid"] or "Unknown"

            events.append(Event(
                source="whatsapp",
                source_id=f"{row['chat_jid']}:{row['id']}",
                event_type="message",
                timestamp=datetime.fromisoformat(row["timestamp"]),
                sender_name=sender,
                sender_id=row["sender"],
                channel_name=chat_name,
                channel_id=row["chat_jid"],
                content=content,
                metadata={
                    "is_from_me": bool(row["is_from_me"]),
                    "media_type": row["media_type"],
                    "has_transcription": bool(row["transcription"]),
                },
            ))

        conn.close()
        log.info("Collected %d WhatsApp messages since %s", len(events), since.isoformat())
        return events
