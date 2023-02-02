# ZeroEventHub server/client wrapper for go

See [ZeroEventHub README](../../README.md) for explanation about
what ZeroEventHub is. This directory contains an implementation
for the HTTP protocol in go.


## Client

We recommend that you keep a cursor in the client's database. Example of
simple single-partition consumption. *Note about the example*:

* Things starting with "my" is supplied by you
* Things starting with "their" is supplied by the service you connect to

```go
// Step 1: Setup
const theirPartitionCount = 1 // documented contract with server
client := zeroeventhub.NewClient(theirServiceUrl, theirPartitionCount)
client = client.WithRequestProcessor(myAuthorizer)  // callback to set Authorization header


// Step 2: Load the cursor from last time we ran 
var cursor string
err := dbconn.QueryRowContext(ctx, `select Cursor from myschema.MyIngestState`).Scan(&cursor)
if err == sql.ErrNoRows {
	cursor = "_last"  // interested in "new" events
} else if err != nil {
	return err
}

// Enter listening loop...

for myStillWantToReadEvents {
	// Step 4: Use ZeroEventHub client. If events have more than one type
	// you may use zeroeventhub.EventPageRaw instead, or implement
	// the zeroeventhub.EventReceiver interface
	var page zeroeventhub.EventPageSingleType[TheirEvent]
	err := client.FetchEvents(
		ctx,
		zeroeventhub.Cursor{PartitionID: 0, Cursor: cursor},
		zeroeventhub.DefaultPageSize,
		&page)
	if err != nil {
		return
	}
	
	// Step 5: Write the efect of changes to our own database and the updated
	// cursor value in the same transaction.
	tx, err := dbconn.dbi.BeginTx(...)
	if err != nil {
		return err
	}
	
	myWriteOfEffectOfEventsToMyDb(ctx, tx, page.Events)
	// Also update cursor IN THE SAME DB TRANSACTION:
	_, err = tx.ExecContext(ctx, `update myschema.MyIngestState set Cursor = @p1`, page.Cursors[0])
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err 
	}
	
	cursor = page.Cursors[0]  // 0 = partition ID; we only have 1 partition -> 0
}

```

### Breaking changes

The URL in the `NewClient` constructor must now be the full URL to the ZeroEventHub endpoint.
Previously, the client would append `/feed/v1` to the URL given.

The `Handler` function now also requires a path argument. Previously, it was hardcoded to
use the path `/feed/v1`.

## Server

This library implements the transport layer, but the basic paginate-over-events
functionality needs implementation specific to your backend and DB.
It is therefore a more complex undertaking read the spec and api_test.go to figure
out how to do it. Eventually we hope to have good examples for the partner
project [mssql-changefeed](https://github.com/vippsas/mssql-changefeed)
that can be emulated. Cosmos implementations are also welcome.
