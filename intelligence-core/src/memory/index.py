import logging
import sqlite3

log = logging.getLogger("intelligence-core.memory.index")


class MemoryIndex:
    def __init__(self, db_path: str):
        self.db_path = db_path
        self._init_db()

    def _init_db(self):
        with sqlite3.connect(self.db_path) as conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS memory_index (
                    entity_type TEXT,
                    entity_name TEXT,
                    file_path TEXT,
                    aliases TEXT,
                    last_updated TIMESTAMP,
                    PRIMARY KEY (entity_type, entity_name)
                )
            """)
            conn.execute("""
                CREATE TABLE IF NOT EXISTS entity_relations (
                    entity_a TEXT,
                    entity_b TEXT,
                    relation_type TEXT,
                    PRIMARY KEY (entity_a, entity_b, relation_type)
                )
            """)

    def upsert(self, entity_type: str, entity_name: str, file_path: str,
               aliases: list[str] | None = None):
        import json
        from datetime import datetime, timezone
        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                """INSERT OR REPLACE INTO memory_index
                (entity_type, entity_name, file_path, aliases, last_updated)
                VALUES (?, ?, ?, ?, ?)""",
                (entity_type, entity_name, file_path,
                 json.dumps(aliases or []),
                 datetime.now(timezone.utc).isoformat()),
            )

    def find_file(self, name: str) -> str | None:
        import json
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            row = conn.execute(
                "SELECT file_path FROM memory_index WHERE entity_name = ?",
                (name.lower(),),
            ).fetchone()
            if row:
                return row["file_path"]

            rows = conn.execute("SELECT entity_name, file_path, aliases FROM memory_index").fetchall()
            for r in rows:
                aliases = json.loads(r["aliases"]) if r["aliases"] else []
                if name.lower() in [a.lower() for a in aliases]:
                    return r["file_path"]
        return None
