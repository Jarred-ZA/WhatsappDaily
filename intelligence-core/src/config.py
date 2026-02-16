import os


DATA_DIR = os.environ.get("DATA_DIR", "/data")
BRIDGE_URL = os.environ.get("BRIDGE_URL", "http://localhost:8080")
BRIDGE_API_KEY = os.environ.get("BRIDGE_API_KEY", "")
ANTHROPIC_API_KEY = os.environ.get("ANTHROPIC_API_KEY", "")
RECIPIENT_JID = os.environ.get("RECIPIENT_JID", "27722066795@s.whatsapp.net")
RECIPIENT_PHONE = os.environ.get("RECIPIENT_PHONE", "27722066795")
SUMMARY_HOUR = int(os.environ.get("SUMMARY_HOUR", "4"))  # UTC hour for daily synthesis
MESSAGE_HOURS = int(os.environ.get("MESSAGE_HOURS", "48"))
DRY_RUN = os.environ.get("DRY_RUN", "false").lower() in ("true", "1", "yes")

# Source tokens (Phase 2+)
GMAIL_P45_CREDENTIALS = os.environ.get("GMAIL_P45_CREDENTIALS", "")
GMAIL_BI_CREDENTIALS = os.environ.get("GMAIL_BI_CREDENTIALS", "")
SLACK_BOT_TOKEN = os.environ.get("SLACK_BOT_TOKEN", "")
GITHUB_TOKEN = os.environ.get("GITHUB_TOKEN", "")
GRANOLA_BEARER_TOKEN = os.environ.get("GRANOLA_BEARER_TOKEN", "")
NOTION_TOKEN = os.environ.get("NOTION_TOKEN", "")

# Paths derived from DATA_DIR
EVENTS_DB_PATH = os.path.join(DATA_DIR, "events.db")
WHATSAPP_DB_PATH = os.path.join(DATA_DIR, "store", "messages.db")
MEMORY_DIR = os.path.join(DATA_DIR, "memory")
CREDENTIALS_DIR = os.path.join(DATA_DIR, "credentials")
COLLECTION_STATE_PATH = os.path.join(DATA_DIR, "memory", "system", "collection_state.json")
