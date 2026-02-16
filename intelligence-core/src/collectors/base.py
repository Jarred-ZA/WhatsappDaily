import json
import logging
import os
from abc import ABC, abstractmethod
from datetime import datetime, timezone
from typing import Optional

from ..models.events import Event

log = logging.getLogger("intelligence-core.collectors")


class Collector(ABC):
    name: str

    def __init__(self, state_file: str):
        self._state_file = state_file

    @abstractmethod
    def collect(self, since: datetime) -> list[Event]:
        """Fetch new data since the given timestamp."""
        ...

    def get_last_collected(self) -> Optional[datetime]:
        state = self._load_state()
        ts = state.get(self.name)
        if ts:
            return datetime.fromisoformat(ts)
        return None

    def set_last_collected(self, ts: datetime):
        state = self._load_state()
        state[self.name] = ts.isoformat()
        os.makedirs(os.path.dirname(self._state_file), exist_ok=True)
        with open(self._state_file, "w") as f:
            json.dump(state, f, indent=2)

    def _load_state(self) -> dict:
        if os.path.exists(self._state_file):
            with open(self._state_file) as f:
                return json.load(f)
        return {}
