import pytest
from zeroeventhub import (
    Cursor,
    Event,
    EventReceiver,
    PageEventReceiver,
)


@pytest.fixture
def page_event_receiver():
    return PageEventReceiver()


async def receive_page_1_events(page_event_receiver: EventReceiver) -> None:
    await page_event_receiver.event(Event(1, {"header1": "abc"}, "event 1 partition 1"))
    await page_event_receiver.event(Event(1, {}, "event 2 partition 1"))
    await page_event_receiver.checkpoint(Cursor(1, "0xf01dab1e"))
    await page_event_receiver.event(Event(2, {"header1": "abc"}, "event 1 partition 2"))
    await page_event_receiver.event(Event(1, {"header1": "def"}, "event 3 partition 1"))
    await page_event_receiver.checkpoint(Cursor(1, "0xFOO"))
    await page_event_receiver.event(Event(2, {"header1": "blah"}, "event 2 partition 2"))
    await page_event_receiver.checkpoint(Cursor(2, "0xBA5EBA11"))


async def test_page_contains_all_received_events_and_checkpoints(
    page_event_receiver,
) -> None:
    """Test that the page contains all received events and checkpoints in order."""
    # act
    await receive_page_1_events(page_event_receiver)

    # assert
    assert page_event_receiver.events == [
        Event(1, {"header1": "abc"}, "event 1 partition 1"),
        Event(1, {}, "event 2 partition 1"),
        Event(2, {"header1": "abc"}, "event 1 partition 2"),
        Event(1, {"header1": "def"}, "event 3 partition 1"),
        Event(2, {"header1": "blah"}, "event 2 partition 2"),
    ]

    assert page_event_receiver.checkpoints == [
        Cursor(1, "0xf01dab1e"),
        Cursor(1, "0xFOO"),
        Cursor(2, "0xBA5EBA11"),
    ]

    assert page_event_receiver.latest_checkpoints == [
        Cursor(1, "0xFOO"),
        Cursor(2, "0xBA5EBA11"),
    ]


async def test_page_is_empty_after_clearing(page_event_receiver) -> None:
    """Test that the page contains no events or checkpoints after being cleared."""
    # arrange
    await receive_page_1_events(page_event_receiver)

    # act
    page_event_receiver.clear()

    # assert
    assert not page_event_receiver.events
    assert not page_event_receiver.checkpoints
    assert not page_event_receiver.latest_checkpoints


async def test_page_contains_all_received_events_and_checkpoints_when_receiving_after_being_cleared(
    page_event_receiver,
) -> None:
    """
    Test that the page contains all received events and checkpoints in order
    from the second page only after the first page was cleared.
    """
    # arrange
    await receive_page_1_events(page_event_receiver)

    # act
    page_event_receiver.clear()
    await page_event_receiver.event(Event(1, None, "event 4 partition 1"))
    await page_event_receiver.checkpoint(Cursor(1, "0x5ca1ab1e"))

    # assert
    assert page_event_receiver.events == [
        Event(1, None, "event 4 partition 1"),
    ]

    assert page_event_receiver.checkpoints == [
        Cursor(1, "0x5ca1ab1e"),
    ]
    assert page_event_receiver.latest_checkpoints == page_event_receiver.checkpoints
