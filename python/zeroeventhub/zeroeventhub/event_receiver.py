"""Module to define the EventReceiver interface"""

from typing import Protocol, Dict, Any, Optional, Union
from collections.abc import AsyncGenerator
from .cursor import Cursor
from .event import Event


class EventReceiver(Protocol):
    """
    EventReceiver is an interface describing an abstraction for handling either
    events or checkpoints.
    Checkpoint in this context is basically a cursor.
    """

    async def event(self, partition_id: int, headers: Optional[Dict[str, str]], data: Any) -> None:
        """
        Event method processes actual events.

        :param partition_id: the partition id
        :param headers: the headers
        :param data: the data
        """

    async def checkpoint(self, partition_id: int, cursor: str) -> None:
        """
        Checkpoint method processes cursors.

        :param partition_id: the partition id
        :param cursor: the cursor
        """


async def receive_events(
    event_receiver: EventReceiver, events: AsyncGenerator[Union[Cursor, Event], None]
) -> None:
    """bridge between the output from the Client fetch_events return value
    and the EventReceiver interface."""
    async for event in events:
        if isinstance(event, Cursor):
            try:
                await event_receiver.checkpoint(event.partition_id, event.cursor)
            except Exception as error:
                raise ValueError("error while receiving checkpoint") from error
        else:
            try:
                await event_receiver.event(event.partition_id, event.headers, event.data)
            except Exception as error:
                raise ValueError("error while receiving event") from error
