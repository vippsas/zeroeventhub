"""Module to make it easy to receive a page of events"""

from typing import Dict, Any, Sequence, Optional, List
from dataclasses import dataclass
from .event_receiver import EventReceiver
from .cursor import Cursor


@dataclass
class Event:
    """All properties received relating to a certain event."""

    partition_id: int
    headers: Optional[Dict[str, str]]
    data: Any


class PageEventReceiver(EventReceiver):
    """
    Receive a page of events
    """

    def __init__(self) -> None:
        """Initialize the PageEventReceiver with empty state."""
        self._events: List[Event] = []
        self._checkpoints: List[Cursor] = []
        self._latest_checkpoints: Dict[int, str] = {}

    def clear(self) -> None:
        """Clear the received events and checkpoints, ready to handle a new page."""
        self._events.clear()
        self._checkpoints.clear()
        self._latest_checkpoints.clear()

    @property
    def events(self) -> Sequence[Event]:
        """Return the page of events received."""
        return self._events

    @property
    def checkpoints(self) -> Sequence[Cursor]:
        """Return the page of checkpoints received."""
        return self._checkpoints

    @property
    def latest_checkpoints(self) -> Sequence[Cursor]:
        """Only return the latest checkpoint for each partition."""
        return [
            Cursor(partition_id, cursor)
            for partition_id, cursor in self._latest_checkpoints.items()
        ]

    def event(self, partition_id: int, headers: Optional[Dict[str, str]], data: Any) -> None:
        """
        Add the given event to the list.

        :param partition_id: the partition id
        :param headers: the headers
        :param data: the data
        """
        self._events.append(Event(partition_id, headers, data))

    def checkpoint(self, partition_id: int, cursor: str) -> None:
        """
        Add the given cursor to the list.

        :param partition_id: the partition id
        :param cursor: the cursor
        """
        self._checkpoints.append(Cursor(partition_id, cursor))
        self._latest_checkpoints[partition_id] = cursor
