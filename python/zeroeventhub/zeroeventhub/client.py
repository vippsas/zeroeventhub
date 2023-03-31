"""Module containing client-side related code for ZeroEventHub"""

import json
from typing import Dict, Optional, Any, Sequence, Union
import requests

from .cursor import Cursor
from .event_receiver import EventReceiver
from .errors import ErrCursorsMissing


class Client:
    """
    Client-side code to query a ZeroEventHub server to fetch events and pass them to the supplied
    EventReceiver instance.
    """

    def __init__(
        self,
        url: str,
        partition_count: int,
        session: Optional[requests.Session] = None,
    ) -> None:

        """
        Initializes a new instance of the Client class.

        :param url: The base URL for the service.
        :param partition_count: The number of partitions the ZeroEventHub server has.
        :param session: An optional session under which to make the HTTP requests.
            This allows one time setup of authentication etc. on the session,
            and increases performance if fetching events frequently due to
            connection pooling.
        """
        self.url = url
        self.partition_count = partition_count
        self._client_owns_session = not bool(session)
        self._session = session or requests.Session()

    def __del__(self) -> None:
        """
        Close the session when this Client instance is destroyed if we created it
        during initialization
        """
        if self._client_owns_session:
            self._session.close()

    @property
    def session(self) -> requests.Session:
        """Return the session being used by this client."""
        return self._session

    def fetch_events(
        self,
        cursors: Sequence[Cursor],
        page_size_hint: Optional[int],
        event_receiver: EventReceiver,
        headers: Optional[Sequence[str]] = None,
    ) -> None:
        """
        Fetch events from the server using the provided context, cursors, page size hint,
        event receiver, and headers.

        :param cursors: A sequence of cursors to be used in the request.
        :param page_size_hint: An optional hint for the page size of the response.
        :param event_receiver: An event receiver to handle the received events.
        :param headers: An optional sequence containing event headers desired in the response.
        :raises APIError: if cursors are missing.
        :raises ValueError: if an exception occurs while the event receiver handles the response.
        :raises requests.exceptions.RequestException: if unable to call the endpoint successfully.
        :raises requests.HTTPError: if response status code does not indicate success.
        :raises json.JSONDecodeError: if a line from the response cannot be decoded into JSON.
        """
        self._validate_inputs(cursors)
        req = self._build_request(cursors, page_size_hint, headers)

        with self.session.send(req, stream=True) as res:
            self._process_response(res, event_receiver)

    def _validate_inputs(self, cursors: Sequence[Cursor]) -> None:
        """
        Validate that the input cursors are not empty.

        :param cursors: A sequence of cursors to be used in the request.
        :raises APIError: if cursors are missing.
        """
        if not cursors:
            raise ErrCursorsMissing

    def _build_request(
        self,
        cursors: Sequence[Cursor],
        page_size_hint: Optional[int],
        headers: Optional[Sequence[str]],
    ) -> requests.PreparedRequest:
        """
        Build the http request using the provided inputs.

        :param cursors: A sequence of cursors to be used in the request.
        :param page_size_hint: An optional hint for the page size of the response.
        :param headers: An optional sequence containing event headers desired in the response.
        :return: the http request
        """
        params: Dict[str, Union[str, int]] = {
            "n": self.partition_count,
        }
        if page_size_hint:
            params["pagesizehint"] = page_size_hint

        for cursor in cursors:
            params[f"cursor{cursor.partition_id}"] = cursor.cursor

        if headers:
            params["headers"] = ",".join(headers)

        request = requests.Request("GET", self.url, params=params)
        return self._session.prepare_request(request)

    def _process_response(self, res: requests.Response, event_receiver: EventReceiver) -> None:
        """
        Process the response from the server.

        :param res: the server response
        :param event_receiver: An event receiver to handle the received events.
        :raises requests.HTTPError: if response status code does not indicate success.
        :raises json.JSONDecodeError: if a line from the response cannot be decoded into JSON.
        :raises ValueError: error while EventReceiver handles checkpoint or event.
        """
        res.raise_for_status()

        for line in res.iter_lines():
            checkpoint_or_event = json.loads(line)
            self._process_checkpoint_or_event(checkpoint_or_event, event_receiver)

    def _process_checkpoint_or_event(
        self, checkpoint_or_event: Dict[str, Any], event_receiver: EventReceiver
    ) -> None:
        """
        Process a line of response from the server.

        :param checkpoint_or_event: A dictionary containing a checkpoint or event.
        :param event_receiver: An event receiver to handle the received events.
        :raises ValueError: if an error occurred in the event receiver while
             the event or checkpoint was being processed.
        """

        if checkpoint_or_event.get("cursor", None) is not None:
            try:
                event_receiver.checkpoint(
                    checkpoint_or_event["partition"], checkpoint_or_event["cursor"]
                )
            except Exception as error:
                raise ValueError("error while receiving checkpoint") from error
        else:
            try:
                event_receiver.event(
                    checkpoint_or_event["partition"],
                    checkpoint_or_event.get("headers", None),
                    checkpoint_or_event["data"],
                )
            except Exception as error:
                raise ValueError("error while receiving event") from error
