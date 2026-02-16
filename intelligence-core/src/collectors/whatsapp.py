import logging
import os
import sqlite3
from datetime import datetime, timedelta, timezone

import httpx

from .base import Collector
from ..models.events import Event
from .. import config

log = logging.getLogger("intelligence-core.collectors.whatsapp")


class WhatsAppCollector(Collector):
    name = "whatsapp"

    def __init__(self, db_path: str, state_file: str):
        super().__init__(state_file)
        self.db_path = db_path

    def collect(self, since: datetime) -> list[Event]:
        # Try direct SQLite first (local dev / shared volume)
        if os.path.exists(self.db_path):
            return self._collect_sqlite(since)
        # Fall back to REST API (Railway)
        return self._collect_api(since)

    def _collect_api(self, since: datetime) -> list[Event]:
        """Fetch messages from the WhatsApp bridge REST API."""
        hours = max(1, int((datetime.now(timezone.utc) - since).total_seconds() / 3600))
        url = f"{config.BRIDGE_URL}/api/messages/recent"
        headers = {}
        if config.BRIDGE_API_KEY:
            headers["X-API-Key"] = config.BRIDGE_API_KEY

        try:
            resp = httpx.get(url, params={"hours": hours}, headers=headers, timeout=30)
            resp.raise_for_status()
            messages = resp.json()
        except httpx.HTTPError as e:
            log.error("Failed to fetch from bridge API: %s", e)
            return []

        if not messages:
            return []

        events = []
        for msg in messages:
            content = msg.get("transcription") or msg.get("content") or ""
            if not content and msg.get("media_type"):
                content = f"[{msg['media_type']}]"
            if not content:
                continue

            sender = "Jarred" if msg.get("is_from_me") else msg.get("sender", "Unknown")
            chat_name = msg.get("chat_name", msg.get("chat_jid", "Unknown"))
            ts = msg.get("timestamp", "")

            try:
                timestamp = datetime.fromisoformat(ts)
            except (ValueError, TypeError):
                timestamp = datetime.now(timezone.utc)

            events.append(Event(
                source="whatsapp",
                source_id=f"{msg.get('chat_jid', '')}:{msg.get('id', '')}",
                event_type="message",
                timestamp=timestamp,
                sender_name=sender,
                sender_id=msg.get("sender"),
                channel_name=chat_name,
                channel_id=msg.get("chat_jid"),
                content=content,
                metadata={
                    "is_from_me": bool(msg.get("is_from_me")),
                    "media_type": msg.get("media_type"),
                    "has_transcription": bool(msg.get("transcription")),
                },
            ))

        log.info("Collected %d WhatsApp messages via API since %s", len(events), since.isoformat())
        return events

    def _collect_sqlite(self, since: datetime) -> list[Event]:
        """Read messages directly from shared SQLite database."""
        try:
            conn = sqlite3.connect(f"file:{self.db_path}?mode=ro", uri=True)
            conn.row_factory = sqlite3.Row
        except sqlite3.OperationalError:
            log.warning("WhatsApp messages.db not accessible at %s, trying API", self.db_path)
            return self._collect_api(since)

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
            return self._collect_api(since)

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
        log.info("Collected %d WhatsApp messages via SQLite since %s", len(events), since.isoformat())
        return events
