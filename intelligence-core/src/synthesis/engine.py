import logging
from datetime import datetime, timedelta, timezone

import anthropic

from .. import config
from ..models.events import Event, EventStore
from ..memory.bank import MemoryBank
from ..memory.updater import apply_memory_updates
from ..synthesis.classifier import classify_event
from ..synthesis.prompts import SYSTEM_PROMPT, build_user_prompt
from ..synthesis.formatter import format_events_digest, extract_briefing

log = logging.getLogger("intelligence-core.synthesis")


class SynthesisEngine:
    def __init__(self, event_store: EventStore, memory_bank: MemoryBank):
        self.event_store = event_store
        self.memory_bank = memory_bank
        self.client = anthropic.Anthropic(api_key=config.ANTHROPIC_API_KEY)

    def run_daily_synthesis(self) -> str | None:
        """Run the full synthesis pipeline. Returns the briefing text or None."""
        since = datetime.now(timezone.utc) - timedelta(hours=config.MESSAGE_HOURS)

        # Step 1: Get all events
        events = self.event_store.get_events_since(since)
        if not events:
            log.info("No events in the last %d hours. Skipping synthesis.", config.MESSAGE_HOURS)
            return None

        # Step 2: Classify events by domain
        for event in events:
            if not event.domain:
                event.domain = classify_event(event)

        log.info(
            "Synthesizing %d events: %d BI Branch, %d P45, %d Personal",
            len(events),
            sum(1 for e in events if e.domain == "bi_branch"),
            sum(1 for e in events if e.domain == "platform45"),
            sum(1 for e in events if e.domain == "personal"),
        )

        # Step 3: Format events digest
        digest = format_events_digest(events)

        # Step 4: Load memory context
        memory_context = self.memory_bank.load_all_memory()

        # Step 5: Build prompt and call Claude
        user_prompt = build_user_prompt(digest, memory_context, config.MESSAGE_HOURS)

        log.info(
            "Calling Claude: %d chars digest, %d chars memory",
            len(digest), len(memory_context),
        )

        message = self.client.messages.create(
            model="claude-sonnet-4-5-20250929",
            max_tokens=4096,
            system=SYSTEM_PROMPT,
            messages=[{"role": "user", "content": user_prompt}],
        )

        response_text = message.content[0].text
        log.info("Claude response: %d chars", len(response_text))

        # Step 6: Extract briefing
        briefing = extract_briefing(response_text)
        log.info("Briefing extracted: %d chars", len(briefing))

        # Step 7: Apply memory updates
        updates = apply_memory_updates(self.memory_bank, response_text)
        if updates:
            log.info("Applied %d memory updates", updates)

        return briefing
