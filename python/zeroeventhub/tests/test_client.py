import pytest
import pytest_asyncio
import httpx
from httpx import AsyncByteStream
from unittest.mock import AsyncMock, MagicMock
from json import JSONDecodeError
from typing import Any, AsyncGenerator, AsyncIterator, Iterable

from zeroeventhub import (
    Client,
    Cursor,
    Event,
    APIError,
    EventReceiver,
    FIRST_CURSOR,
    LAST_CURSOR,
    ALL_HEADERS,
    receive_events,
)


class IteratorStream(AsyncByteStream):
    def __init__(self, stream: Iterable[bytes]):
        self.stream = stream

    async def __aiter__(self) -> AsyncIterator[bytes]:
        for chunk in self.stream:
            yield chunk


@pytest.fixture
def mock_event_receiver():
    receiver_mock = MagicMock(spec=EventReceiver)
    receiver_mock.event = AsyncMock()
    receiver_mock.checkpoint = AsyncMock()
    return receiver_mock


@pytest_asyncio.fixture
async def client():
    url = "https://example.com/feed/v1"
    partition_count = 2
    async with httpx.AsyncClient() as httpx_client:
        yield Client(url, partition_count, httpx_client)


@pytest.mark.parametrize(
    ("page_size_hint", "headers"),
    [(10, ["header1", "header2"]), (0, None), (5, ["header1"]), (0, ["header1"])],
)
async def test_events_fetched_successfully_when_there_are_multiple_lines_in_response(
    client, mock_event_receiver, page_size_hint, headers, respx_mock
):
    """
    Test that fetch_events does not raise an error when successfully called.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=200,
            headers={"content_type": "application/x-ndjson"},
            content=IteratorStream(
                [
                    b"""{ "partition": 1, "cursor": "5" }\n""",
                    b"""{ "partition": 1, "headers": {}, "data": "some data"}\n""",
                ]
            ),
        )
    )

    # act
    await receive_events(mock_event_receiver, client.fetch_events(cursors, page_size_hint, headers))

    # assert
    mock_event_receiver.event.assert_called_once_with(Event(1, {}, "some data"))
    mock_event_receiver.checkpoint.assert_called_once_with(Cursor(1, "5"))


async def test_raises_apierror_when_fetch_events_with_missing_cursors(client):
    """
    Test that fetch_events raises a ValueError when cursors are missing.
    """

    # arrange
    page_size_hint = 10
    headers = ["header1", "header2"]
    cursors = None

    # act & assert
    with pytest.raises(APIError) as excinfo:
        await async_generator_to_list(client.fetch_events(cursors, page_size_hint, headers))

    # assert
    assert "cursors are missing" in str(excinfo.value)
    assert excinfo.value.status() == 400


async def test_raises_http_error_when_fetch_events_with_unexpected_response(client, respx_mock):
    """
    Test that fetch_events raises a HTTPError when the response status
    code is not 2xx
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=404,
        )
    )

    # act & assert that a HTTPError is raised
    with pytest.raises(httpx.HTTPError) as excinfo:
        await async_generator_to_list(client.fetch_events(cursors, page_size_hint, headers))

    # assert
    assert str(excinfo.value).startswith(f"Client error '404 Not Found' for url '{client.url}?")


async def test_raises_error_when_exception_while_parsing_checkpoint(client, respx_mock):
    """
    Test that fetch_events raises a ValueError when the checkpoint returned
    from the server cannot be parsed.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=200,
            headers={"content_type": "application/x-ndjson"},
            content="""{ "cursor": "0" }""",  # NOTE: partition is missing
        )
    )

    # act & assert
    with pytest.raises(ValueError, match="error while parsing checkpoint"):
        await async_generator_to_list(client.fetch_events(cursors, page_size_hint, headers))


async def test_raises_error_when_exception_while_parsing_event(client, respx_mock):
    """
    Test that fetch_events raises a ValueError when the event returned
    from the server cannot be parsed.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=200,
            headers={"content_type": "application/x-ndjson"},
            content="""{ "data": "" }""",  # NOTE: partition is missing
        )
    )

    # act & assert
    with pytest.raises(ValueError, match="error while parsing event"):
        await async_generator_to_list(client.fetch_events(cursors, page_size_hint, headers))


async def test_raises_error_when_exception_while_receiving_checkpoint(
    client, mock_event_receiver, respx_mock
):
    """
    Test that fetch_events raises a ValueError when the checkpoint method
    on the event receiver returns an error.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=200,
            headers={"content_type": "application/x-ndjson"},
            content="""{ "partition": 0, "cursor": "0" }""",
        )
    )

    mock_event_receiver.checkpoint.side_effect = Exception("error while receiving checkpoint")

    # act & assert
    with pytest.raises(ValueError, match="error while receiving checkpoint"):
        await receive_events(
            mock_event_receiver, client.fetch_events(cursors, page_size_hint, headers)
        )


async def test_raises_error_when_exception_while_receiving_event(
    client, mock_event_receiver, respx_mock
):
    """
    Test that fetch_events raises a ValueError when the event method
    on the event receiver returns an error.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=200,
            headers={"content_type": "application/x-ndjson"},
            content="""{"partition": 0, "headers": {}, "data": "some data"}\n""",
        )
    )
    mock_event_receiver.event.side_effect = Exception("some error while processing the event")

    # act & assert
    with pytest.raises(ValueError, match="error while receiving event"):
        await receive_events(
            mock_event_receiver, client.fetch_events(cursors, page_size_hint, headers)
        )

    # assert
    mock_event_receiver.event.assert_called()


async def test_fetch_events_succeeds_when_response_is_empty(
    client, mock_event_receiver, respx_mock
):
    """
    Test that fetch_events gracefully handles an empty response.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = None

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=204,
            headers={"content_type": "application/x-ndjson"},
            content="",
        )
    )

    # act
    await receive_events(mock_event_receiver, client.fetch_events(cursors, page_size_hint, headers))

    # assert that the event and checkpoint methods were not called
    mock_event_receiver.event.assert_not_called()
    mock_event_receiver.checkpoint.assert_not_called()


async def test_raises_error_when_response_contains_invalid_json_line(
    client, mock_event_receiver, respx_mock
):
    """
    Test that fetch_events raises a JSONDecodeError when the response contains a non-empty
    line which is not valid JSON.
    """

    # arrange
    cursors = [Cursor(0, "cursor1"), Cursor(1, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    respx_mock.get(client.url).mock(
        return_value=httpx.Response(
            status_code=200,
            headers={"content_type": "application/x-ndjson"},
            content="""{"partition": 1,"cursor": "5"}\ninvalid json""",
        )
    )

    # act & assert
    with pytest.raises(JSONDecodeError):
        await receive_events(
            mock_event_receiver, client.fetch_events(cursors, page_size_hint, headers)
        )

    # assert that the checkpoint method was called for the first response line
    mock_event_receiver.checkpoint.assert_called_once_with(Cursor(1, "5"))


async def async_generator_to_list(input: AsyncGenerator[Any, None]) -> list[Any]:
    return [item async for item in input]
