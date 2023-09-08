"""Module to define the event dataclass"""

from dataclasses import dataclass
from typing import Any, Dict, Optional


@dataclass
class Event:
    """All properties received relating to a certain event."""

    partition_id: int
    headers: Optional[Dict[str, str]]
    data: Any
