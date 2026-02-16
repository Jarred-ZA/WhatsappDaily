import logging
import os
import sys
import time
from datetime import datetime, timedelta, timezone

from apscheduler.schedulers.blocking import BlockingScheduler

from . import config
from .models.events import EventStore
from .collectors.whatsapp import WhatsAppCollector
from .memory.bank import MemoryBank
from .synthesis.engine import SynthesisEngine
from .synthesis.classifier import classify_event
from .delivery.whatsapp import send_whatsapp_message

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
log = logging.getLogger("intelligence-core")


def collect_whatsapp(event_store: EventStore, collector: WhatsAppCollector):
    """Run WhatsApp collection sweep."""
    since = collector.get_last_collected()
    if since is None:
        since = datetime.now(timezone.utc) - timedelta(hours=config.MESSAGE_HOURS)

    start = time.time()
    try:
        events = collector.collect(since)
        # Classify each event
        for event in events:
            event.domain = classify_event(event)
        stored = event_store.store_events(events)
        duration = time.time() - start
        event_store.log_collection("whatsapp", stored, "success", duration=duration)
        if stored:
            log.info("WhatsApp: stored %d new events (%.1fs)", stored, duration)
        collector.set_last_collected(datetime.now(timezone.utc))
    except Exception as e:
        duration = time.time() - start
        event_store.log_collection("whatsapp", 0, "failed", str(e), duration)
        log.error("WhatsApp collection failed: %s", e)


def run_synthesis(event_store: EventStore, memory_bank: MemoryBank):
    """Run the daily synthesis pipeline."""
    log.info("=== Starting daily synthesis ===")

    engine = SynthesisEngine(event_store, memory_bank)

    try:
        briefing = engine.run_daily_synthesis()
    except Exception as e:
        log.error("Synthesis failed: %s", e)
        return

    if not briefing:
        log.info("No briefing generated (no events)")
        return

    if config.DRY_RUN:
        log.info("DRY RUN - Briefing would be sent:")
        print("\n" + "=" * 50)
        print(briefing)
        print("=" * 50 + "\n")
    else:
        # Split into multiple messages if needed (WhatsApp limit ~4096 chars)
        if len(briefing) > 4000:
            parts = _split_message(briefing, 4000)
            for i, part in enumerate(parts):
                send_whatsapp_message(part)
                log.info("Sent part %d/%d", i + 1, len(parts))
        else:
            send_whatsapp_message(briefing)

    log.info("=== Synthesis complete ===")


def _split_message(text: str, max_len: int) -> list[str]:
    """Split a message at line boundaries."""
    parts = []
    current = ""
    for line in text.split("\n"):
        if len(current) + len(line) + 1 > max_len:
            parts.append(current.strip())
            current = line + "\n"
        else:
            current += line + "\n"
    if current.strip():
        parts.append(current.strip())
    return parts


def main():
    log.info("=== Intelligence Core V2 ===")
    log.info("Data dir: %s", config.DATA_DIR)
    log.info("Bridge URL: %s", config.BRIDGE_URL)
    log.info("Summary hour: %d UTC", config.SUMMARY_HOUR)
    log.info("Message hours: %d", config.MESSAGE_HOURS)
    log.info("Dry run: %s", config.DRY_RUN)

    # Ensure directories exist
    os.makedirs(config.DATA_DIR, exist_ok=True)

    # Initialize stores
    event_store = EventStore(config.EVENTS_DB_PATH)
    memory_bank = MemoryBank(config.MEMORY_DIR)
    memory_bank.ensure_structure()

    wa_collector = WhatsAppCollector(
        db_path=config.WHATSAPP_DB_PATH,
        state_file=config.COLLECTION_STATE_PATH,
    )

    # If run with --once flag, just do one collection + synthesis and exit
    if "--once" in sys.argv:
        log.info("Running one-shot collection + synthesis")
        collect_whatsapp(event_store, wa_collector)
        run_synthesis(event_store, memory_bank)
        return

    # Set up scheduler
    scheduler = BlockingScheduler()

    # WhatsApp collection: every 30 minutes
    scheduler.add_job(
        collect_whatsapp, "interval", minutes=30,
        args=[event_store, wa_collector],
        id="collect_whatsapp", name="WhatsApp collection",
    )

    # Daily synthesis: at SUMMARY_HOUR UTC
    scheduler.add_job(
        run_synthesis, "cron", hour=config.SUMMARY_HOUR, minute=0,
        args=[event_store, memory_bank],
        id="daily_synthesis", name="Daily synthesis",
    )

    # Run initial collection immediately
    log.info("Running initial collection...")
    collect_whatsapp(event_store, wa_collector)

    log.info(
        "Scheduler started. Next synthesis at %02d:00 UTC. "
        "WhatsApp collection every 30 min.",
        config.SUMMARY_HOUR,
    )

    try:
        scheduler.start()
    except (KeyboardInterrupt, SystemExit):
        log.info("Shutting down...")


if __name__ == "__main__":
    main()
