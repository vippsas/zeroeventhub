"""Module to define the EventReceiver interface"""

from typing import Protocol, Dict, Any, Optional


class EventReceiver(Protocol):
    """
    EventReceiver is an interface describing an abstraction for handling either
    events or checkpoints.
    Checkpoint in this context is basically a cursor.
    """

    def event(self, partition_id: int, headers: Optional[Dict[str, str]], data: Any) -> None:
        """
        Event method processes actual events.

        :param partition_id: the partition id
        :param headers: the headers
        :param data: the data
        """

    def checkpoint(self, partition_id: int, cursor: str) -> None:
        """
        Checkpoint method processes cursors.

        :param partition_id: the partition id
        :param cursor: the cursor
        """
