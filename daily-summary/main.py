import os
import sys
import json
import logging
from datetime import datetime, timezone

import anthropic
import requests

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
log = logging.getLogger("daily-summary")

BRIDGE_URL = os.environ.get("BRIDGE_INTERNAL_URL", "http://localhost:8080")
BRIDGE_API_KEY = os.environ.get("BRIDGE_API_KEY", "")
ANTHROPIC_API_KEY = os.environ.get("ANTHROPIC_API_KEY", "")
RECIPIENT_PHONE = os.environ.get("RECIPIENT_PHONE", "27722066795")
MESSAGE_HOURS = int(os.environ.get("MESSAGE_HOURS", "48"))
DRY_RUN = os.environ.get("DRY_RUN", "false").lower() in ("true", "1", "yes")

SYSTEM_PROMPT = """You are Jarred's personal WhatsApp assistant. Your job is to analyze his recent WhatsApp conversations and produce a concise daily action-item summary.

Focus on extracting:
1. Commitments Jarred made to others (things he said he'd do)
2. Requests others made of him (things people asked him to do or follow up on)
3. Pending decisions he needs to make
4. Unresolved follow-ups and open threads that need his attention
5. Deadlines or time-sensitive items mentioned

Rules:
- Be concise and actionable - this will be read on a phone
- Use plain text only, no markdown formatting
- Group items by priority: URGENT first, then HIGH, then NORMAL
- Include the chat/person name for context
- Skip small talk, memes, and irrelevant messages
- If a voice note transcription is available, treat it as regular message content
- Maximum 1800 characters total
- Start with a brief greeting like "Morning Jarred! Here's your daily summary:"
- End with a count of total action items"""


def fetch_recent_messages():
    """Fetch recent messages from the WhatsApp bridge."""
    url = f"{BRIDGE_URL}/api/messages/recent"
    params = {"hours": MESSAGE_HOURS}
    headers = {}
    if BRIDGE_API_KEY:
        headers["X-API-Key"] = BRIDGE_API_KEY

    log.info("Fetching messages from %s (last %d hours)...", url, MESSAGE_HOURS)
    resp = requests.get(url, params=params, headers=headers, timeout=30)
    resp.raise_for_status()

    messages = resp.json()
    if messages is None:
        messages = []

    log.info("Fetched %d messages", len(messages))
    return messages


def format_messages_digest(messages):
    """Group messages by chat and format as a readable digest for Claude."""
    chats = {}
    for msg in messages:
        chat_name = msg.get("chat_name", msg.get("chat_jid", "Unknown"))
        if chat_name not in chats:
            chats[chat_name] = []

        sender = "Jarred" if msg.get("is_from_me") else msg.get("sender", "Unknown")
        content = msg.get("content", "")
        transcription = msg.get("transcription", "")
        media_type = msg.get("media_type", "")
        timestamp = msg.get("timestamp", "")

        # Use transcription for voice notes, otherwise use content
        display_content = transcription if transcription else content
        if not display_content and media_type:
            display_content = f"[{media_type}]"

        if display_content:
            chats[chat_name].append({
                "sender": sender,
                "content": display_content,
                "timestamp": timestamp,
            })

    # Format as text digest
    lines = []
    for chat_name, msgs in sorted(chats.items(), key=lambda x: len(x[1]), reverse=True):
        if not msgs:
            continue
        lines.append(f"\n=== {chat_name} ({len(msgs)} messages) ===")
        for m in msgs:
            ts = m["timestamp"][:16] if m["timestamp"] else ""
            lines.append(f"[{ts}] {m['sender']}: {m['content']}")

    return "\n".join(lines)


def analyze_with_claude(digest):
    """Send the message digest to Claude for analysis."""
    if not ANTHROPIC_API_KEY:
        log.error("ANTHROPIC_API_KEY not set")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY)

    log.info("Sending %d chars of digest to Claude for analysis...", len(digest))

    message = client.messages.create(
        model="claude-sonnet-4-5-20250929",
        max_tokens=1024,
        system=SYSTEM_PROMPT,
        messages=[
            {
                "role": "user",
                "content": f"Here are Jarred's WhatsApp messages from the last {MESSAGE_HOURS} hours. Please analyze them and produce his daily action-item summary:\n\n{digest}",
            }
        ],
    )

    summary = message.content[0].text
    log.info("Claude generated summary (%d chars)", len(summary))
    return summary


def send_whatsapp_message(summary):
    """Send the summary via WhatsApp through the bridge."""
    url = f"{BRIDGE_URL}/api/send"
    headers = {"Content-Type": "application/json"}
    if BRIDGE_API_KEY:
        headers["X-API-Key"] = BRIDGE_API_KEY

    payload = {
        "recipient": RECIPIENT_PHONE,
        "message": summary,
    }

    log.info("Sending summary to %s...", RECIPIENT_PHONE)
    resp = requests.post(url, json=payload, headers=headers, timeout=30)
    resp.raise_for_status()

    result = resp.json()
    if result.get("success"):
        log.info("Summary sent successfully!")
    else:
        log.error("Failed to send summary: %s", result.get("message"))
        sys.exit(1)


def main():
    log.info("=== WhatsApp Daily Summary ===")
    log.info("Time: %s", datetime.now(timezone.utc).isoformat())
    log.info("Bridge URL: %s", BRIDGE_URL)
    log.info("Recipient: %s", RECIPIENT_PHONE)
    log.info("Message hours: %d", MESSAGE_HOURS)
    log.info("Dry run: %s", DRY_RUN)

    # Step 1: Fetch recent messages
    messages = fetch_recent_messages()
    if not messages:
        log.info("No messages found in the last %d hours. Nothing to summarize.", MESSAGE_HOURS)
        return

    # Step 2: Format digest
    digest = format_messages_digest(messages)
    if not digest.strip():
        log.info("No meaningful content to summarize.")
        return

    # Step 3: Analyze with Claude
    summary = analyze_with_claude(digest)

    # Step 4: Send or print
    if DRY_RUN:
        log.info("DRY RUN - Summary would be sent to %s:", RECIPIENT_PHONE)
        print("\n" + "=" * 50)
        print(summary)
        print("=" * 50 + "\n")
    else:
        send_whatsapp_message(summary)

    log.info("Done!")


if __name__ == "__main__":
    main()
