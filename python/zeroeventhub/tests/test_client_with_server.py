from collections.abc import AsyncGenerator, Sequence
from typing import Any

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse
from pytest_mock import MockerFixture
from zeroeventhub import (
    ALL_HEADERS,
    FIRST_CURSOR,
    Client,
    Cursor,
    DataReader,
    Event,
    ZeroEventHubFastApiHandler,
)

app = FastAPI()


class FakeDataReader(DataReader):
    async def get_data(
        self,
        cursors: Sequence[Cursor],
        headers: Sequence[str] | None,
        page_size: int | None,
    ) -> AsyncGenerator[dict[str, Any], Any]:
        yield {"this method will be replaced": "by mocks"}


@app.get("/person/feed/v1")
async def feed(request: Request) -> StreamingResponse:
    dr = FakeDataReader()
    api_handler = ZeroEventHubFastApiHandler(data_reader=dr, server_partition_count=1)
    return api_handler.handle(request)


@pytest_asyncio.fixture
async def client():
    url = "person/feed/v1"
    partition_count = 1
    async with httpx.AsyncClient(app=app, base_url="http://example/") as httpx_client:
        yield Client(url, partition_count, httpx_client)


@pytest.mark.parametrize(
    "feed",
    [
        [
            Event(
                0,
                {},
                {
                    "id": 1000,
                    "type": "PersonNameChanged",
                    "when": "2023-09-26T11:23:30.472906Z",
                    "person_id": 876,
                    "new_name": {
                        "first_name": "Fred",
                        "surname": "Bloggs",
                    },
                },
            ),
            Event(
                0,
                None,
                {
                    "id": 1001,
                    "type": "PersonNameChanged",
                    "when": "2023-09-27T07:34:09.261815Z",
                    "person_id": 932,
                    "new_name": {
                        "first_name": "Joe",
                        "surname": "King",
                    },
                },
            ),
            Cursor(0, "1002"),
        ],
    ],
)
@pytest.mark.parametrize(
    ("page_size_hint", "headers"),
    [
        (None, None),
        (None, ALL_HEADERS),
        (1000, ALL_HEADERS),
        (1000, None),
        (None, ["header1", "header2"]),
    ],
)
async def test_client_can_successfully_fetch_events_from_server(
    client: Client,
    feed: Sequence[Event | Cursor],
    mocker: MockerFixture,
    page_size_hint: int | None,
    headers: Sequence[str] | None,
) -> None:
    """Test that fetch_events retrieves all the events and cursors the server responds with."""
    # arrange
    cursors = [Cursor(0, FIRST_CURSOR)]

    async def yield_data(
        _cursors: Sequence[Cursor],
        _headers: dict[str, Any] | None,
        _page_size_hint: int | None,
    ) -> AsyncGenerator[dict[str, Any], None]:
        for item in feed:
            if isinstance(item, Cursor):
                yield {"partition": item.partition_id, "cursor": item.cursor}
            elif isinstance(item, Event):
                yield {
                    "partition": item.partition_id,
                    "headers": item.headers,
                    "data": item.data,
                }

    get_data_mock = mocker.patch.object(FakeDataReader, "get_data")
    get_data_mock.side_effect = yield_data

    # act
    result = [
        item
        async for item in client.fetch_events(
            cursors, headers=headers, page_size_hint=page_size_hint
        )
    ]

    # assert
    assert result == feed
    get_data_mock.assert_called_once_with(
        cursors, list(headers) if headers else headers, page_size_hint
    )
