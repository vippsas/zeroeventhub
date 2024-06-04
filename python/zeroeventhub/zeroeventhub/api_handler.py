"""Api handlers definition."""

import json
from collections.abc import AsyncGenerator, Generator
from typing import Any

from fastapi import HTTPException, Request, status
from fastapi.responses import StreamingResponse

from .cursor import Cursor
from .data_reader import DataReader


class ZeroEventHubFastApiHandler:
    """Handler for ZeroEventHub from server side using fastapi."""

    def __init__(
        self,
        data_reader: DataReader,
        server_partition_count: int,
    ) -> None:
        """Initialize the ZeroEventHubFastApiHandler with DataReader."""
        self.data_reader = data_reader
        self.server_partition_count = server_partition_count

    def validate(self, request: Request) -> Any:
        """Validate all required parameters and its format.
        Return the expected parameter structure for next step processing.
        """
        query_params = request.query_params
        n_param = query_params.get("n")
        if n_param is None:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST, detail="Parameter n not found"
            )
        try:
            client_partition_count = int(n_param)
        except ValueError as err:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST, detail="Invalid parameter n"
            ) from err
        if client_partition_count != self.server_partition_count:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail="Partition count doesn't match as expected",
            )
        cursor_arr = []
        for i in range(client_partition_count):
            cursor_param_name = f"cursor{i}"
            cursor_param_value = query_params.get(cursor_param_name)
            if cursor_param_value:
                cursor_arr.append(Cursor(i, cursor_param_value))
        if not cursor_arr:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail="Cursor parameter is missing",
            )

        page_size_hint_param = query_params.get("pagesizehint")
        page_size_hint = None
        if page_size_hint_param:
            try:
                page_size_hint = int(page_size_hint_param)
            except ValueError as err:
                raise HTTPException(
                    status_code=status.HTTP_400_BAD_REQUEST,
                    detail="Invalid parameter pagesizehint",
                ) from err

        headers_param = query_params.get("headers")
        headers = None
        if headers_param:
            headers = headers_param.rstrip(",").split(",")

        return {
            "cursors": cursor_arr,
            "headers": headers,
            "pagesizehint": page_size_hint,
        }

    async def generate_response_format(
        self,
        data_gen: Generator[dict[str, Any], Any, Any] | AsyncGenerator[dict[str, Any], Any],
    ) -> AsyncGenerator[bytes, Any]:
        """Generate the response format for the client."""
        # pylint: disable=C0209
        if isinstance(data_gen, AsyncGenerator):
            async for data in data_gen:
                yield f"{json.dumps(data)}\n".encode()
        else:
            for data in data_gen:
                yield f"{json.dumps(data)}\n".encode()

    def handle(self, request: Request) -> StreamingResponse:
        """Handle the request after validation.
        Return final response to the client.
        """
        validated_data = self.validate(request)
        cursors_arr = validated_data["cursors"]
        headers = validated_data["headers"]
        data_gen = self.data_reader.get_data(cursors_arr, headers, validated_data["pagesizehint"])
        response_gen = self.generate_response_format(data_gen)
        return StreamingResponse(response_gen, media_type="application/x-ndjson")
