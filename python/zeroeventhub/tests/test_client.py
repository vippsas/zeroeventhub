import pytest
import responses
import requests
import mock_protocol
from unittest import mock
from json import JSONDecodeError

from zeroeventhub import (
    Client,
    Cursor,
    APIError,
    EventReceiver,
    FIRST_CURSOR,
    LAST_CURSOR,
    ALL_HEADERS,
)


@pytest.fixture
def mock_event_receiver():
    return mock_protocol.from_protocol(EventReceiver)


@pytest.fixture
def client():
    url = "https://example.com"
    partition_count = 2
    return Client(url, partition_count)


@pytest.mark.parametrize("page_size_hint,headers", [
    (10, ["header1", "header2"]),
    (0, None),
    (5, ["header1"]),
    (0, ["header1"])
])
@responses.activate
def test_events_fetched_successfully_when_there_are_multiple_lines_in_response(
    client, mock_event_receiver, page_size_hint, headers
):
    """
    Test that fetch_events does not raise an error when successfully called.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        content_type="application/json",
        body="""{ "partition": 1, "cursor": "5" }
        { "partition": 1, "headers": {}, "data": "some data"}\n""",
        status=200,
    )

    # act
    client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)

    # assert
    mock_event_receiver.event.assert_called_once_with(1, {}, "some data")
    mock_event_receiver.checkpoint.assert_called_once_with(1, '5')


def test_raises_apierror_when_fetch_events_with_missing_cursors(client, mock_event_receiver):
    """
    Test that fetch_events raises a ValueError when cursors are missing.
    """

    # arrange
    page_size_hint = 10
    headers = ["header1", "header2"]
    cursors = None

    # act & assert
    with pytest.raises(APIError) as excinfo:
        client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)

    # assert
    assert "cursors are missing" in str(excinfo.value)
    assert excinfo.value.status() == 400


@responses.activate
def test_raises_http_error_when_fetch_events_with_unexpected_response(client, mock_event_receiver):
    """
    Test that fetch_events raises a HTTPError when the response status
    code is not 2xx
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    responses.add(responses.GET, f"{client.url}/feed/v1", status=404)

    # act & assert that a HTTPError is raised
    with pytest.raises(requests.HTTPError) as excinfo:
        client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)

    # assert
    assert str(excinfo.value).startswith(
        f"404 Client Error: Not Found for url: {client.url}/feed/v1?"
    )


@responses.activate
def test_raises_error_when_exception_while_receiving_checkpoint(client, mock_event_receiver):
    """
    Test that fetch_events raises a ValueError when the checkpoint method
    on the event receiver returns an error.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        json={
            "partition": 0,
            "cursor": "0",
        },
        status=200,
    )

    mock_event_receiver.checkpoint.side_effect = Exception("error while receiving checkpoint")

    # act & assert
    with pytest.raises(ValueError, match="error while receiving checkpoint"):
        client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)


@responses.activate
def test_raises_error_when_exception_while_receiving_event(client, mock_event_receiver):
    """
    Test that fetch_events raises a ValueError when the event method
    on the event receiver returns an error.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        json={"partition": 0, "headers": {}, "data": "some data"},
        status=200,
    )

    mock_event_receiver.event.side_effect = Exception("error while receiving event")

    # act & assert
    with pytest.raises(ValueError, match="error while receiving event"):
        client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)

    # assert
    mock_event_receiver.event.assert_called()


@responses.activate
def test_fetch_events_succeeds_when_response_is_empty(client, mock_event_receiver):
    """
    Test that fetch_events gracefully handles an empty response.
    """

    # arrange
    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = None

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        content_type="application/json",
        body="",
        status=204,
    )

    # act
    client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)

    # assert that the event and checkpoint methods were not called
    mock_event_receiver.event.assert_not_called()
    mock_event_receiver.checkpoint.assert_not_called()


@responses.activate
def test_raises_error_when_response_contains_invalid_json_line(client, mock_event_receiver):
    """
    Test that fetch_events raises a JSONDecodeError when the response contains a non-empty
    line which is not valid JSON.
    """

    # arrange
    cursors = [Cursor(0, "cursor1"), Cursor(1, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        body="""{"partition": 1,"cursor": "5"}\ninvalid json""",
        status=200,
    )

    # act & assert
    with pytest.raises(JSONDecodeError):
        client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)

    # assert that the checkpoint method was called for the first response line
    mock_event_receiver.checkpoint.assert_called_once_with(1, "5")


@responses.activate
def test_owned_session_destroyed_when_client_destroyed(mock_event_receiver, mocker):
    """
    Test that the client-owned session is closed when the client is destroyed
    """

    # arrange
    url = "https://example.com"
    partition_count = 2
    session_mock = mocker.MagicMock(spec_set=requests.Session)
    with mock.patch("zeroeventhub.client.requests.Session") as create_session_mock:
        create_session_mock.return_value = session_mock
        client = Client(url, partition_count)
        create_session_mock.assert_called_once()
        assert session_mock == client.session

    cursors = [Cursor(1, "cursor1"), Cursor(2, "cursor2")]
    page_size_hint = 10
    headers = ["header1", "header2"]

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        body="""{"partition": 0,"cursor": 0}\n""",
        status=200,
    )

    # act
    client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)
    del client

    # assert
    session_mock.send.assert_called_once()
    session_mock.close.assert_called_once()


@responses.activate
def test_provided_session_not_destroyed_when_client_destroyed(mock_event_receiver, mocker):
    """
    Test that the provided session is not closed when the client is destroyed
    """

    # arrange
    url = "https://example.com"
    partition_count = 2
    session_mock = mocker.MagicMock(spec_set=requests.Session)
    with mock.patch("zeroeventhub.client.requests.Session") as create_session_mock:
        client = Client(url, partition_count, session_mock)
        create_session_mock.assert_not_called()

    cursors = [Cursor(1, FIRST_CURSOR), Cursor(2, LAST_CURSOR)]
    page_size_hint = None
    headers = ALL_HEADERS

    responses.add(
        responses.GET,
        f"{client.url}/feed/v1",
        body="""{"partition": 1,"cursor": "123"}\n""",
        status=200,
    )

    # act
    client.fetch_events(cursors, page_size_hint, mock_event_receiver, headers)
    del client

    # assert
    session_mock.send.assert_called_once()
    session_mock.close.assert_not_called()
