"""Module to define the event dataclass."""

from dataclasses import dataclass
from typing import Any


@dataclass
class Event:
    """All properties received relating to a certain event."""

    partition_id: int
    headers: dict[str, str] | None
    data: Any
