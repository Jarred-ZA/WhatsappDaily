import logging
import re
from .bank import MemoryBank

log = logging.getLogger("intelligence-core.memory.updater")


def apply_memory_updates(bank: MemoryBank, claude_response: str) -> int:
    """Parse MEMORY_UPDATE blocks from Claude's response and apply them."""
    pattern = re.compile(
        r"MEMORY_UPDATE_START\s*\n"
        r"FILE:\s*([^\n]+)\n"
        r"SECTION:\s*([^\n]+)\n"
        r"ACTION:\s*(replace|append)\s*\n"
        r"CONTENT:\s*\n(.*?)\n?"
        r"MEMORY_UPDATE_END",
        re.DOTALL,
    )

    updates = pattern.findall(claude_response)
    applied = 0

    for file_path, section, action, content in updates:
        file_path = file_path.strip()
        section = section.strip()
        action = action.strip().lower()
        content = content.strip()

        existing = bank.load_file(file_path)

        if existing is None:
            # Create new file with this section
            new_content = f"# {file_path.split('/')[-1].replace('.md', '').title()}\n\n## {section}\n{content}\n"
            bank.save_file(file_path, new_content)
            applied += 1
            continue

        section_header = f"## {section}"
        if section_header in existing:
            if action == "replace":
                existing = _replace_section(existing, section, content)
            elif action == "append":
                existing = _append_to_section(existing, section, content)
        else:
            existing = existing.rstrip() + f"\n\n## {section}\n{content}\n"

        bank.save_file(file_path, existing)
        applied += 1

    if applied:
        log.info("Applied %d memory updates", applied)
    return applied


def _replace_section(text: str, section: str, new_content: str) -> str:
    """Replace the content of a section, keeping the header."""
    lines = text.split("\n")
    result = []
    in_target = False
    replaced = False

    for line in lines:
        if line.strip() == f"## {section}":
            result.append(line)
            result.append(new_content)
            in_target = True
            replaced = True
            continue

        if in_target and line.startswith("## "):
            in_target = False

        if not in_target:
            result.append(line)

    return "\n".join(result)


def _append_to_section(text: str, section: str, new_content: str) -> str:
    """Append content to the end of a section."""
    lines = text.split("\n")
    result = []
    in_target = False
    appended = False

    for i, line in enumerate(lines):
        if line.strip() == f"## {section}":
            in_target = True

        if in_target and not appended:
            next_is_new_section = (
                i + 1 < len(lines) and lines[i + 1].startswith("## ")
            ) or i == len(lines) - 1

            if next_is_new_section:
                result.append(line)
                result.append(new_content)
                in_target = False
                appended = True
                continue

        result.append(line)

    if in_target and not appended:
        result.append(new_content)

    return "\n".join(result)
