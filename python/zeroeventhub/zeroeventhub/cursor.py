"""This module defines a Cursor dataclass for use by the client and server."""

from dataclasses import dataclass


@dataclass
class Cursor:
    """
    A dataclass encapsulating both the partition ID and the actual cursor within this partition.

    :param partition_id: The partition ID
    :param cursor: The cursor within the partition
    """

    partition_id: int
    cursor: str


FIRST_CURSOR = "_first"
"""FIRST_CURSOR is a special cursor: starts at the first event."""

LAST_CURSOR = "_last"
"""LAST_CURSOR is a special cursor: starts at the last available event."""
