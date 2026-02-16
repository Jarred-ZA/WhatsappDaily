import logging

import httpx

from .. import config

log = logging.getLogger("intelligence-core.delivery")


def send_whatsapp_message(message: str) -> bool:
    """Send a message via the WhatsApp bridge REST API."""
    url = f"{config.BRIDGE_URL}/api/send"
    headers = {"Content-Type": "application/json"}
    if config.BRIDGE_API_KEY:
        headers["X-API-Key"] = config.BRIDGE_API_KEY

    payload = {
        "recipient": config.RECIPIENT_PHONE,
        "message": message,
    }

    try:
        resp = httpx.post(url, json=payload, headers=headers, timeout=30)
        resp.raise_for_status()
        result = resp.json()
        if result.get("success"):
            log.info("Briefing sent to %s", config.RECIPIENT_PHONE)
            return True
        else:
            log.error("Bridge returned failure: %s", result.get("message"))
            return False
    except httpx.HTTPError as e:
        log.error("Failed to send briefing: %s", e)
        return False
