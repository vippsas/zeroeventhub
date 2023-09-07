"""Module to define the DataReader interface"""

from typing import Protocol, Sequence, Any, Optional, Generator, Dict, AsyncGenerator, Union
from .cursor import Cursor

# pylint: disable=R0903


class DataReader(Protocol):
    """
    DataReader is an interface describing an abstraction for reading data for ZeroEventHub response
    and generate header values based on the list of header keys requested from client
    """

    def get_data(
        self, cursors: Sequence[Cursor], headers: Optional[Sequence[str]], page_size: Optional[int]
    ) -> Union[Generator[Dict[str, Any], None, None], AsyncGenerator[Dict[str, Any], None]]:
        """
        Read a page of events at server side for the given cursors.

        :param cursors: the requested partition and start point for receiving events
        :param headers: the header keys to be be fullfiled with values
        :param page_size: page size of the return data
        """
