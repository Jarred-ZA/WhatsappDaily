import re
from ..models.events import Event


def format_events_digest(events: list[Event]) -> str:
    """Group events by source and channel, format as readable digest."""
    by_source: dict[str, dict[str, list[Event]]] = {}

    for event in events:
        source = event.source
        channel = event.channel_name or "Direct"
        if source not in by_source:
            by_source[source] = {}
        if channel not in by_source[source]:
            by_source[source][channel] = []
        by_source[source][channel].append(event)

    lines = []

    source_labels = {
        "whatsapp": "WHATSAPP",
        "gmail": "EMAIL",
        "slack": "SLACK",
        "github": "GITHUB",
        "granola": "MEETING NOTES",
        "notion": "NOTION",
    }

    for source, channels in sorted(by_source.items()):
        label = source_labels.get(source, source.upper())
        lines.append(f"\n{'=' * 40}")
        lines.append(f"{label}")
        lines.append(f"{'=' * 40}")

        for channel, channel_events in sorted(
            channels.items(), key=lambda x: len(x[1]), reverse=True
        ):
            lines.append(f"\n--- {channel} ({len(channel_events)} items) ---")
            for evt in channel_events:
                ts = evt.timestamp.strftime("%m-%d %H:%M") if evt.timestamp else ""
                sender = evt.sender_name or "Unknown"
                if evt.title:
                    lines.append(f"[{ts}] {sender}: {evt.title}")
                    if evt.content:
                        # Truncate long content
                        content = evt.content[:500]
                        if len(evt.content) > 500:
                            content += "..."
                        lines.append(f"  {content}")
                else:
                    content = evt.content or ""
                    if len(content) > 500:
                        content = content[:500] + "..."
                    lines.append(f"[{ts}] {sender}: {content}")

    return "\n".join(lines)


def extract_briefing(claude_response: str) -> str:
    """Extract the briefing text from Claude's response."""
    match = re.search(
        r"BRIEFING_START\n(.*?)\nBRIEFING_END",
        claude_response,
        re.DOTALL,
    )
    if match:
        return match.group(1).strip()

    # Fallback: if no markers, take everything before MEMORY_UPDATE_START
    if "MEMORY_UPDATE_START" in claude_response:
        return claude_response[:claude_response.index("MEMORY_UPDATE_START")].strip()

    return claude_response.strip()
