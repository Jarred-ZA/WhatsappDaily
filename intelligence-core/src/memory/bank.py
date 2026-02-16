import logging
import os
from pathlib import Path

log = logging.getLogger("intelligence-core.memory")


class MemoryBank:
    def __init__(self, memory_dir: str):
        self.memory_dir = Path(memory_dir)

    def ensure_structure(self):
        for subdir in ["people", "projects", "organizations", "system"]:
            (self.memory_dir / subdir).mkdir(parents=True, exist_ok=True)

    def load_file(self, relative_path: str) -> str | None:
        path = self.memory_dir / relative_path
        if path.exists():
            return path.read_text(encoding="utf-8")
        return None

    def save_file(self, relative_path: str, content: str):
        path = self.memory_dir / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        log.info("Updated memory file: %s", relative_path)

    def load_domain(self, domain: str) -> str:
        """Load all memory files relevant to a domain and return as combined text."""
        parts = []

        # Load organization file
        org_file = self._domain_to_org(domain)
        if org_file:
            content = self.load_file(f"organizations/{org_file}")
            if content:
                parts.append(content)

        # Load all people files
        for path in sorted(self.memory_dir.glob("people/*.md")):
            if path.name.startswith("_"):
                continue
            content = path.read_text(encoding="utf-8")
            parts.append(content)

        # Load all project files
        for path in sorted(self.memory_dir.glob("projects/*.md")):
            if path.name.startswith("_"):
                continue
            content = path.read_text(encoding="utf-8")
            parts.append(content)

        return "\n\n---\n\n".join(parts) if parts else ""

    def load_all_memory(self) -> str:
        """Load all memory files as context for synthesis."""
        parts = []
        for category in ["organizations", "people", "projects"]:
            category_dir = self.memory_dir / category
            if not category_dir.exists():
                continue
            for path in sorted(category_dir.glob("*.md")):
                if path.name.startswith("_"):
                    continue
                content = path.read_text(encoding="utf-8")
                if content.strip():
                    parts.append(f"[{category}/{path.name}]\n{content}")
        return "\n\n---\n\n".join(parts) if parts else "(No memory files yet)"

    def list_files(self) -> list[str]:
        files = []
        for path in self.memory_dir.rglob("*.md"):
            if path.name.startswith("_"):
                continue
            files.append(str(path.relative_to(self.memory_dir)))
        return sorted(files)

    def _domain_to_org(self, domain: str) -> str | None:
        mapping = {
            "bi_branch": "bi-branch.md",
            "platform45": "platform45.md",
        }
        return mapping.get(domain)
