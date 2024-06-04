"""Module containing client-side related code for ZeroEventHub."""

import json
from collections.abc import AsyncGenerator, Sequence
from typing import Any

import httpx

from .cursor import Cursor
from .errors import ErrCursorsMissing
from .event import Event
from .response_line_iterator import aiter_lines


class Client:
    """Client-side code to query a ZeroEventHub server to fetch events."""

    def __init__(
        self,
        url: str,
        partition_count: int,
        http_client: httpx.AsyncClient,
    ) -> None:
        """
        Initializes a new instance of the Client class.

        :param url: The base URL for the service.
        :param partition_count: The number of partitions the ZeroEventHub server has.
        :param http_client: A httpx AsyncClient under which to make the HTTP requests.
            This allows one time setup of authentication etc. on the session,
            and increases performance if fetching events frequently due to
            connection pooling.
        """
        self.url = url
        self.partition_count = partition_count
        self._http_client = http_client

    @property
    def http_client(self) -> httpx.AsyncClient:
        """Return the http_client being used by this client."""
        return self._http_client

    async def fetch_events(
        self,
        cursors: Sequence[Cursor],
        page_size_hint: int | None = None,
        headers: Sequence[str] | None = None,
    ) -> AsyncGenerator[Event | Cursor, None]:
        """
        Fetch events from the server using the provided cursors, page size hint and
        desired headers.

        :param cursors: A sequence of cursors to be used in the request.
        :param page_size_hint: An optional hint for the page size of the response.
        :param headers: An optional sequence containing event headers desired in the response.
        :raises APIError: if cursors are missing.
        :raises ValueError: if an exception occurs while the event receiver handles the response.
        :raises httpx.RequestError: if unable to call the endpoint successfully.
        :raises httpx.HTTPError: if response status code does not indicate success.
        :raises json.JSONDecodeError: if a line from the response cannot be decoded into JSON.
        """
        self._validate_inputs(cursors)
        params = self._build_request_params(cursors, page_size_hint, headers)

        async with self._http_client.stream("GET", self.url, params=params) as res:
            async for event_or_checkpoint in self._process_response(res):
                yield event_or_checkpoint

    def _validate_inputs(self, cursors: Sequence[Cursor]) -> None:
        """
        Validate that the input cursors are not empty.

        :param cursors: A sequence of cursors to be used in the request.
        :raises APIError: if cursors are missing.
        """
        if not cursors:
            raise ErrCursorsMissing

    def _build_request_params(
        self,
        cursors: Sequence[Cursor],
        page_size_hint: int | None,
        headers: Sequence[str] | None,
    ) -> dict[str, str | int]:
        """
        Build the http request using the provided inputs.

        :param cursors: A sequence of cursors to be used in the request.
        :param page_size_hint: An optional hint for the page size of the response.
        :param headers: An optional sequence containing event headers desired in the response.
        :return: the http request
        """
        params: dict[str, str | int] = {
            "n": self.partition_count,
        }
        if page_size_hint:
            params["pagesizehint"] = page_size_hint

        for cursor in cursors:
            params[f"cursor{cursor.partition_id}"] = cursor.cursor

        if headers:
            params["headers"] = ",".join(headers)

        return params

    async def _process_response(self, res: httpx.Response) -> AsyncGenerator[Event | Cursor, None]:
        """
        Process the response from the server.

        :param res: the server response
        :raises httpx.HTTPError: if response status code does not indicate success.
        :raises json.JSONDecodeError: if a line from the response cannot be decoded into JSON.
        :raises ValueError: error while EventReceiver handles checkpoint or event.
        """
        res.raise_for_status()

        async for line in aiter_lines(res, "\n"):
            if not line:
                continue
            yield self._parse_checkpoint_or_event(line)

    def _parse_checkpoint_or_event(self, raw_line: str) -> Event | Cursor:
        """
        Parse a line of response from the server.

        :param raw_line: The raw JSON line from the server
        :raises ValueError: if an error occurred parsing the json line into an event or checkpoint.
        """
        checkpoint_or_event: dict[str, Any] = json.loads(raw_line)

        if (cursor := checkpoint_or_event.get("cursor")) is not None:
            try:
                return Cursor(
                    partition_id=checkpoint_or_event["partition"],
                    cursor=cursor,
                )
            except Exception as error:
                msg = "error while parsing checkpoint"
                raise ValueError(msg) from error
        else:
            try:
                return Event(
                    partition_id=checkpoint_or_event["partition"],
                    headers=checkpoint_or_event.get("headers"),
                    data=checkpoint_or_event["data"],
                )
            except Exception as error:
                msg = "error while parsing event"
                raise ValueError(msg) from error
