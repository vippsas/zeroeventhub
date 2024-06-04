"""Module to make it easy to receive a page of events."""

from collections.abc import Sequence

from .cursor import Cursor
from .event import Event
from .event_receiver import EventReceiver


class PageEventReceiver(EventReceiver):
    """Receive a page of events."""

    def __init__(self) -> None:
        """Initialize the PageEventReceiver with empty state."""
        self._events: list[Event] = []
        self._checkpoints: list[Cursor] = []
        self._latest_checkpoints: dict[int, Cursor] = {}

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
        return list(self._latest_checkpoints.values())

    async def event(self, event: Event) -> None:
        """
        Add the given event to the list.

        :param event: the event
        """
        self._events.append(event)

    async def checkpoint(self, checkpoint: Cursor) -> None:
        """
        Add the given checkpoint to the list.

        :param checkpoint: the cursor to use as a checkpoint to continue processing from later
        """
        self._checkpoints.append(checkpoint)
        self._latest_checkpoints[checkpoint.partition_id] = checkpoint
