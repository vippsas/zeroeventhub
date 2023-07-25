# ZeroEventHub

This README file contains information specific to the Python port of the ZeroEventHub.
Please see the [main readme file](../../README.md) for an overview of what this project is about.

## Client

We recommend that you store the latest checkpoint/cursor for each partition in the client's
database. Example of simple single-partition consumption. *Note about the example*:

* Things starting with "my" is supplied by you
* Things starting with "their" is supplied by the service you connect to

```python
# Step 1: Setup
their_partition_count = 1 # documented contract with server
zeh_session = requests.Session() # you can setup the authentication on the session
client = zeroeventhub.Client(their_service_url, their_partition_count, zeh_session)

# Step 2: Load the cursors from last time we ran
cursors = my_get_cursors_from_db()
if not cursors:
    # we have never run before, so we can get all events with FIRST_CURSOR
    # (if we just want to receive new events from now, we would use LAST_CURSOR)
    cursors = [
        zeroeventhub.Cursor(partition_id, zeroeventhub.FIRST_CURSOR)
        for partition_id in range(their_partition_count)
    ]

# Step 3: Enter listening loop...
page_of_events = PageEventReceiver()
while myStillWantToReadEvents:
    # Step 4: Use ZeroEventHub client to fetch the next page of events.
    client.fetch_events(
        cursors,
        my_page_size_hint,
        page_of_events
    )

    # Step 5: Write the effect of changes to our own database and the updated
    #         cursor value in the same transaction.
    with db.begin_transaction() as tx:
        my_write_effect_of_events_to_db(tx, page_of_events.events)

        my_write_cursors_to_db(tx, page_of_events.latest_checkpoints)

        tx.commit()

    cursors = page_of_events.latest_checkpoints

    page_of_events.clear()
```

## Development

To run the test suite, assuming you already have Python 3.10 or later installed and on your `PATH`:
```sh
pip install poetry==1.5.1
poetry config virtualenvs.in-project true
poetry install --sync
poetry run coverage run --branch -m pytest
poetry run coverage html
```

Then, you can open the `htmlcov/index.html` file in your browser to look at the code coverage report.

Also, to pass the CI checks, you may want to run the following before pushing your changes:

```sh
poetry run black tests/ zeroeventhub/
poetry run pylint ./zeroeventhub/
poetry run flake8
poetry run mypy
```
