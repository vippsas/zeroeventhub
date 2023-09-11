package zeroeventhub

import (
	"encoding/json"
	"io"
)

// Envelope contains event headers (standard string map) and the event data (any JSON-serializable struct)
type Envelope struct {
	PartitionID int               `json:"partition"`
	Headers     map[string]string `json:"headers,omitempty"`
	Data        json.RawMessage   `json:"data,omitempty"`
}

type TypedEnvelope[T any] struct {
	PartitionID int               `json:"partition"`
	Headers     map[string]string `json:"headers,omitempty"`
	Data        T                 `json:"data"`
}

// NDJSONEventSerializer implements EventReceiver by emitting Newline-Delimited-JSON to a writer.
type NDJSONEventSerializer struct {
	encoder *json.Encoder
	writer  io.Writer
}

func NewNDJSONEventSerializer(writer io.Writer) *NDJSONEventSerializer {
	return &NDJSONEventSerializer{
		encoder: json.NewEncoder(writer),
		writer:  writer,
	}
}

func (s NDJSONEventSerializer) writeNdJsonLine(item interface{}) error {
	return s.encoder.Encode(item)
}

func (s NDJSONEventSerializer) Checkpoint(partitionID int, cursor string) error {
	return s.writeNdJsonLine(Cursor{
		PartitionID: partitionID,
		Cursor:      cursor,
	})
}

func (s NDJSONEventSerializer) Event(partitionID int, headers map[string]string, data json.RawMessage) error {
	return s.writeNdJsonLine(Envelope{
		PartitionID: partitionID,
		Headers:     headers,
		Data:        data,
	})
}

var _ EventReceiver = &NDJSONEventSerializer{}

// EventPageRaw implements EventReceiver by storing the events and new cursor in memory.
// The data is stored as json.RawMessage. See EventPageSingleType for a simple way
// to use a single struct.
type EventPageRaw struct {
	Events  []Envelope
	Cursors map[int]string
}

func (page *EventPageRaw) Checkpoint(partitionID int, cursor string) error {
	if page.Cursors == nil {
		page.Cursors = make(map[int]string)
	}
	page.Cursors[partitionID] = cursor
	return nil
}

func (page *EventPageRaw) Event(partitionID int, h map[string]string, d json.RawMessage) error {
	page.Events = append(page.Events, Envelope{
		PartitionID: partitionID,
		Headers:     h,
		Data:        d,
	})
	return nil
}

// EventPageSingleType is like EventPageRaw, but parses the JSON into a single struct
// type. Useful if all the events on the feed have the same format.
type EventPageSingleType[T any] struct {
	Events  []TypedEnvelope[T]
	Cursors map[int]string
}

func (page *EventPageSingleType[T]) Checkpoint(partitionID int, cursor string) error {
	if page.Cursors == nil {
		page.Cursors = make(map[int]string)
	}
	page.Cursors[partitionID] = cursor
	return nil
}

func (page *EventPageSingleType[T]) Event(partitionID int, h map[string]string, d json.RawMessage) error {
	var e TypedEnvelope[T]
	e.PartitionID = partitionID
	e.Headers = h
	if err := json.Unmarshal(d, &e.Data); err != nil {
		return err
	}
	page.Events = append(page.Events, e)
	return nil
}
