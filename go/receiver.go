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

func (s NDJSONEventSerializer) Checkpoint(cursor string) error {
	type CursorMessage struct {
		Cursor string `json:"cursor"`
	}
	return s.writeNdJsonLine(CursorMessage{Cursor: cursor})
}

func (s NDJSONEventSerializer) Event(data json.RawMessage) error {
	type EventMessage struct {
		Data json.RawMessage `json:"data"`
	}
	return s.writeNdJsonLine(EventMessage{Data: data})
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
	Events []T
	Cursor string
}

func (page *EventPageSingleType[T]) Checkpoint(cursor string) error {
	page.Cursor = cursor
	return nil
}

func (page *EventPageSingleType[T]) Event(d json.RawMessage) error {
	var data T
	if err := json.Unmarshal(d, &data); err != nil {
		return err
	}
	page.Events = append(page.Events, data)
	return nil
}
