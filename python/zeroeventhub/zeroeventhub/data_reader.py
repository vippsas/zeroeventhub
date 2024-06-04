"""Module to define the DataReader interface."""

from collections.abc import AsyncGenerator, Generator, Sequence
from typing import Any, Protocol

from .cursor import Cursor

# pylint: disable=R0903


class DataReader(Protocol):
    """
    DataReader is an interface describing an abstraction for reading data for ZeroEventHub response
    and generate header values based on the list of header keys requested from client.
    """

    def get_data(
        self,
        cursors: Sequence[Cursor],
        headers: Sequence[str] | None,
        page_size: int | None,
    ) -> Generator[dict[str, Any], None, None] | AsyncGenerator[dict[str, Any], None]:
        """
        Read a page of events at server side for the given cursors.

        :param cursors: the requested partition and start point for receiving events
        :param headers: the header keys to be be fullfiled with values
        :param page_size: page size of the return data
        """
        ...
