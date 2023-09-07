from typing import Any, Dict, List, Optional, Sequence, Generator, AsyncGenerator, Union
import json
import pytest
from unittest import mock
from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse
from httpx import AsyncClient
from fastapi.testclient import TestClient
from zeroeventhub.cursor import Cursor
from zeroeventhub.api_handler import ZeroEventHubFastApiHandler
from zeroeventhub.data_reader import DataReader

app = FastAPI()


class FakeAsyncDataReader(DataReader):
    async def get_data(
        self, cursors: Sequence[Cursor], headers: Optional[Sequence[str]], page_size: Optional[int]
    ) -> Union[Generator[Dict[str, Any], Any, Any], AsyncGenerator[Dict[str, Any], Any]]:
        header_dict = {}
        if headers:
            for header in headers:
                header_dict[header] = header
        event_list_p1 = ["e1", "e2", "e3"]
        event_list_p2 = ["e4", "e5", "e6"]
        for cursor in cursors:
            if cursor.partition_id == 0:
                for event in event_list_p1:
                    yield {
                        "partition": cursor.partition_id,
                        "headers": header_dict,
                        "data": event,
                    }
                # yield checkpoint after all data
                yield {"cursor": "c0", "partition": cursor.partition_id}
            elif cursor.partition_id == 1:
                for event in event_list_p2:
                    yield {
                        "partition": cursor.partition_id,
                        "headers": header_dict,
                        "data": event,
                    }
                # yield checkpoint after all data
                yield {"cursor": "c1", "partition": cursor.partition_id}


class FakeDataReader(DataReader):
    def get_data(
        self, cursors: Sequence[Cursor], headers: Optional[Sequence[str]], page_size: Optional[int]
    ) -> Union[Generator[Dict[str, Any], Any, Any], AsyncGenerator[Dict[str, Any], Any]]:
        header_dict = {}
        if headers:
            for header in headers:
                header_dict[header] = header
        event_list_p1 = ["e1", "e2", "e3"]
        event_list_p2 = ["e4", "e5", "e6"]
        for cursor in cursors:
            if cursor.partition_id == 0:
                for event in event_list_p1:
                    yield {
                        "partition": cursor.partition_id,
                        "headers": header_dict,
                        "data": event,
                    }
                # yield checkpoint after all data
                yield {"cursor": "c0", "partition": cursor.partition_id}
            elif cursor.partition_id == 1:
                for event in event_list_p2:
                    yield {
                        "partition": cursor.partition_id,
                        "headers": header_dict,
                        "data": event,
                    }
                # yield checkpoint after all data
                yield {"cursor": "c1", "partition": cursor.partition_id}


@app.get("/feed/v1")
async def validate_endpoint(request: Request) -> StreamingResponse:
    dr = FakeDataReader()
    api_handler = ZeroEventHubFastApiHandler(data_reader=dr, server_partition_count=2)
    return api_handler.handle(request)


@app.get("/feed/v2")
async def validate_async_endpoint(request: Request) -> StreamingResponse:
    dr = FakeAsyncDataReader()
    api_handler = ZeroEventHubFastApiHandler(data_reader=dr, server_partition_count=2)
    return api_handler.handle(request)


@pytest.mark.asyncio
async def test_request_handler_single_cursor():
    async with AsyncClient(app=app, base_url="http://test") as client:
        response = await client.get("/feed/v1?n=2&cursor0=c1")
        assert response.status_code == 200
        parsed_data = [json.loads(line) async for line in response.aiter_lines()]

    assert parsed_data == [
        {"partition": 0, "headers": {}, "data": "e1"},
        {"partition": 0, "headers": {}, "data": "e2"},
        {"partition": 0, "headers": {}, "data": "e3"},
        {"cursor": "c0", "partition": 0},
    ]


@pytest.mark.asyncio
async def test_request_handler_double_cursors():
    async with AsyncClient(app=app, base_url="http://test") as client:
        response = await client.get("/feed/v1?n=2&cursor0=c0&cursor1=c1")
        assert response.status_code == 200
        parsed_data = [json.loads(line) async for line in response.aiter_lines()]
    assert parsed_data == [
        {"partition": 0, "headers": {}, "data": "e1"},
        {"partition": 0, "headers": {}, "data": "e2"},
        {"partition": 0, "headers": {}, "data": "e3"},
        {"cursor": "c0", "partition": 0},
        {"partition": 1, "headers": {}, "data": "e4"},
        {"partition": 1, "headers": {}, "data": "e5"},
        {"partition": 1, "headers": {}, "data": "e6"},
        {"cursor": "c1", "partition": 1},
    ]


@pytest.mark.asyncio
async def test_request_handler_full_parameter_set():
    async with AsyncClient(app=app, base_url="http://test") as client:
        response = await client.get(
            "/feed/v1?n=2&cursor0=c0&cursor1=c1&headers=h1,h2,h3&pagesizehint=10"
        )
        assert response.status_code == 200
        parsed_data = [json.loads(line) async for line in response.aiter_lines()]
    assert parsed_data == [
        {"partition": 0, "headers": {"h1": "h1", "h2": "h2", "h3": "h3"}, "data": "e1"},
        {"partition": 0, "headers": {"h1": "h1", "h2": "h2", "h3": "h3"}, "data": "e2"},
        {"partition": 0, "headers": {"h1": "h1", "h2": "h2", "h3": "h3"}, "data": "e3"},
        {"cursor": "c0", "partition": 0},
        {"partition": 1, "headers": {"h1": "h1", "h2": "h2", "h3": "h3"}, "data": "e4"},
        {"partition": 1, "headers": {"h1": "h1", "h2": "h2", "h3": "h3"}, "data": "e5"},
        {"partition": 1, "headers": {"h1": "h1", "h2": "h2", "h3": "h3"}, "data": "e6"},
        {"cursor": "c1", "partition": 1},
    ]


@pytest.mark.asyncio
async def test_request_handler_cursor0_skipping():
    async with AsyncClient(app=app, base_url="http://test") as client:
        response = await client.get("/feed/v1?n=2&cursor1=c1")
        assert response.status_code == 200
        parsed_data = [json.loads(line) async for line in response.aiter_lines()]
    assert parsed_data == [
        {"partition": 1, "headers": {}, "data": "e4"},
        {"partition": 1, "headers": {}, "data": "e5"},
        {"partition": 1, "headers": {}, "data": "e6"},
        {"cursor": "c1", "partition": 1},
    ]


def test_no_n_param():
    client = TestClient(app)
    response = client.get("/feed/v1?cursor0=c0&cursor1=c1&headers=h1,h2,h3")
    assert response.status_code == 400
    assert response.json() == {"detail": "Parameter n not found"}


def test_invalid_n_param():
    with mock.patch.object(FakeDataReader, "get_data") as mocked_get_data:
        client = TestClient(app)
        response = client.get("/feed/v1?n=a&cursor0=c0&cursor1=c1&headers=h1,h2,h3")
        assert response.status_code == 400
        assert response.json() == {"detail": "Invalid parameter n"}
        mocked_get_data.assert_not_called()


def test_invalid_cursor_param():
    client = TestClient(app)
    response = client.get("/feed/v1?n=2&cursor2=c0&headers=h1,h2,h3")
    assert response.status_code == 400
    assert response.json() == {"detail": "Cursor parameter is missing"}


@pytest.mark.asyncio
async def test_work_with_async_endpoint():
    async with AsyncClient(app=app, base_url="http://test") as client:
        response = await client.get("/feed/v2?n=2&cursor0=c1")
        assert response.status_code == 200
        parsed_data = [json.loads(line) async for line in response.aiter_lines()]

    assert parsed_data == [
        {"partition": 0, "headers": {}, "data": "e1"},
        {"partition": 0, "headers": {}, "data": "e2"},
        {"partition": 0, "headers": {}, "data": "e3"},
        {"cursor": "c0", "partition": 0},
    ]
