package zeroeventhub

import (
	"context"
	"encoding/json"
)

const (
	// FirstCursor is a special cursor: starts at the first event.
	FirstCursor = "_first"
	// LastCursor is a special cursor: starts at the last available event.
	LastCursor = "_last"
	// DefaultPageSize is used to figure out whether the caller hasn't provided any page size hint.
	DefaultPageSize = 0
	// All is a special value for `headers` argument representing a request for returning "all headers available".
	All = "_all"
)

// Cursor is a struct encapsulating both the partition ID and the actual cursor within this partition.
type Cursor struct {
	PartitionID int    `json:"partition"`
	Cursor      string `json:"cursor"`
}

// EventReceiver is an interface describing an abstraction for handling either events, or checkpoints.
// Checkpoint in this context is basically a cursor.
type EventReceiver interface {
	// Event method processes actual events.
	Event(Data json.RawMessage) error
	// Checkpoint method processes cursors.
	Checkpoint(cursor string) error
}

type Options struct {
	PageSizeHint int
}

// EventFetcher is a generic-based interface providing a contract for fetching events: both for the server side and
// client side implementations.
type EventFetcher interface {
	// FetchEvents method accepts array of Cursor's along with an optional page size hint and an EventReceiver.
	// Pass pageSizeHint = 0 for having the server choose a default / no hint.
	// Optional `headers` argument specifies headers to be returned, or none, if it's absent.
	FetchEvents(ctx context.Context, token string, partitionID int, cursor string, receiver EventReceiver, options Options) error
}
