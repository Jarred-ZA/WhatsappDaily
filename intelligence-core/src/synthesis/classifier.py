from ..models.events import Event

DOMAIN_RULES = {
    "bi_branch": {
        "people": ["patrick", "henry", "reagan"],
        "email_domains": ["bibranch.co.za"],
        "keywords": ["ecv", "dayone", "day one", "bi branch", "bibranch"],
    },
    "platform45": {
        "people": ["maro", "justin", "shaun", "wayne"],
        "email_domains": ["platform45.com"],
        "slack_channels": ["yebo", "readygolf", "hagglz", "carma"],
        "keywords": ["yebo", "carma", "readygolf", "ready golf", "hagglz", "p45", "platform45", "platform 45"],
    },
}


def classify_event(event: Event) -> str:
    """Classify an event into a domain using rule-based matching."""
    text = " ".join(filter(None, [
        event.sender_name,
        event.channel_name,
        event.title,
        event.content[:200] if event.content else None,
    ])).lower()

    for domain, rules in DOMAIN_RULES.items():
        # Check people
        for person in rules.get("people", []):
            if person in text:
                return domain

        # Check email domains
        sender_id = (event.sender_id or "").lower()
        for email_domain in rules.get("email_domains", []):
            if email_domain in sender_id:
                return domain

        # Check Slack channels
        channel = (event.channel_name or "").lower()
        for slack_channel in rules.get("slack_channels", []):
            if slack_channel in channel:
                return domain

        # Check keywords
        for keyword in rules.get("keywords", []):
            if keyword in text:
                return domain

    return "personal"
