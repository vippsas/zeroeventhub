# ZeroEventHub

This README file contains information specific to the Python port of the ZeroEventHub.
Please see the [main readme file](../../README.md) for an overview of what this project is about.

## Client

We recommend that you store the latest checkpoint/cursor for each partition in the client's
database. Example of simple single-partition consumption. *Note about the example*:

* Things starting with "my" is supplied by you
* Things starting with "their" is supplied by the service you connect to

```python
>>> import zeroeventhub
>>> import httpx
>>> import asyncio
>>> from typing import Sequence
>>> from unittest.mock import MagicMock, Mock, PropertyMock

>>> my_db = MagicMock()
>>> my_person_event_repository = Mock()
>>> my_person_event_repository.read_cursors_from_db.return_value = None

# Step 1: Setup
>>> their_partition_count = 1 # documented contract with server
>>> their_service_url = "https://localhost:8192/person/feed/v1"
>>> my_zeh_session = httpx.AsyncClient() # you can setup the authentication on the session
>>> client = zeroeventhub.Client(their_service_url, their_partition_count, my_zeh_session)

# Step 2: Load the cursors from last time we ran
>>> cursors = my_person_event_repository.read_cursors_from_db()
>>> if not cursors:
...     # we have never run before, so we can get all events with FIRST_CURSOR
...     # (if we just want to receive new events from now, we would use LAST_CURSOR)
...     cursors = [
...         zeroeventhub.Cursor(partition_id, zeroeventhub.FIRST_CURSOR)
...         for partition_id in range(their_partition_count)
...     ]

# Step 3: Enter listening loop...
>>> my_still_want_to_read_events = PropertyMock(side_effect=[True, False])

>>> async def poll_for_events(cursors: Sequence[zeroeventhub.Cursor]) -> None:
...     page_of_events = zeroeventhub.PageEventReceiver()
...     while my_still_want_to_read_events():
...         # Step 4: Use ZeroEventHub client to fetch the next page of events.
...         await zeroeventhub.receive_events(page_of_events,
...             client.fetch_events(cursors),
...         )
...
...         # Step 5: Write the effect of changes to our own database and the updated
...         #         cursor value in the same transaction.
...         with my_db.begin_transaction() as tx:
...             my_person_event_repository.write_effect_of_events_to_db(tx, page_of_events.events)
...             my_person_event_repository.write_cursors_to_db(tx, page_of_events.latest_checkpoints)
...             tx.commit()
...
...         cursors = page_of_events.latest_checkpoints
...         page_of_events.clear()

>>> asyncio.run(poll_for_events(cursors))

```

## Server

This library makes it easy to setup a zeroeventhub feed endpoint with FastAPI.

```python
>>> from typing import Annotated, Any, AsyncGenerator, Dict, Optional, Sequence
>>> from fastapi import Depends, FastAPI, Request
>>> from fastapi.responses import StreamingResponse
>>> from zeroeventhub import (
...     Cursor,
...     DataReader,
...     ZeroEventHubFastApiHandler,
... )
>>> from unittest.mock import Mock

>>> app = FastAPI()

>>> PersonEventRepository = Mock

>>> class PersonDataReader(DataReader):
...     def __init__(self, person_event_repository: PersonEventRepository) -> None:
...         self._person_event_repository = person_event_repository
...
...     def get_data(
...         self, cursors: Sequence[Cursor], headers: Optional[Sequence[str]], page_size: Optional[int]
...     ) -> AsyncGenerator[Dict[str, Any], Any]:
...         return (
...             self._person_event_repository.get_events_since(cursors[0].cursor)
...             .take(page_size)
...             .with_headers(headers)
...         )

>>> def get_person_data_reader() -> PersonDataReader:
...     return PersonDataReader(PersonEventRepository())

>>> PersonDataReaderDependency = Annotated[
...    PersonDataReader,
...    Depends(get_person_data_reader, use_cache=True),
... ]

>>> @app.get("person/feed/v1")
... async def feed(request: Request, person_data_reader: PersonDataReaderDependency) -> StreamingResponse:
...     api_handler = ZeroEventHubFastApiHandler(data_reader=person_data_reader, server_partition_count=1)
...     return api_handler.handle(request)

```

## Development

To run the test suite, assuming you already have Python 3.10 or later installed and on your `PATH`:
```sh
pip install poetry==1.8.4
poetry config virtualenvs.in-project true
poetry install --sync
poetry run coverage run --branch -m pytest
poetry run coverage html
```

Then, you can open the `htmlcov/index.html` file in your browser to look at the code coverage report.

Also, to pass the CI checks, you may want to run the following before pushing your changes:

```sh
poetry run ruff format
poetry run ruff check
poetry run pyright
```
