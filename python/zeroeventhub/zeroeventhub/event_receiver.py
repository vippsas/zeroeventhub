"""Module to define the EventReceiver interface."""

from collections.abc import AsyncGenerator
from typing import Protocol

from .cursor import Cursor
from .event import Event


class EventReceiver(Protocol):
    """
    EventReceiver is an interface describing an abstraction for handling either
    events or checkpoints.
    Checkpoint in this context is basically a cursor.
    """

    async def event(self, event: Event) -> None:
        """
        Event method processes actual events.

        :param event: the details of the event which has been received from the server
        """

    async def checkpoint(self, checkpoint: Cursor) -> None:
        """
        Checkpoint method processes cursors.

        :param checkpoint: the checkpoint which was received from the server
        """


async def receive_events(
    event_receiver: EventReceiver, events: AsyncGenerator[Cursor | Event, None]
) -> None:
    """Bridge between the output from the Client fetch_events return value
    and the EventReceiver interface.
    """
    async for event_or_checkpoint in events:
        if isinstance(event_or_checkpoint, Cursor):
            await event_receiver.checkpoint(event_or_checkpoint)
        else:
            await event_receiver.event(event_or_checkpoint)
