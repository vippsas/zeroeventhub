package zeroeventhub

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/http/httptest"
	"strconv"
)

var logger *logrus.Logger

func init() {
	logger = logrus.StandardLogger()
	logger.SetLevel(logrus.DebugLevel)
}

func Server(publisher EventPublisher) *httptest.Server {
	handlers := HTTPHandlers{
		EventPublisher: publisher,
		LoggerFromRequest: func(*http.Request) logrus.FieldLogger {
			return logger
		},
	}

	routingHandler := func(w http.ResponseWriter, r *http.Request) {
		// expose the feed on "testfeed"
		if r.URL.Path == "/testfeed" {
			handlers.DiscoveryHandler(w, r)
			return
		} else if r.URL.Path == "/testfeed/events" {
			handlers.EventsHandler(w, r)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}

	return httptest.NewServer(http.HandlerFunc(routingHandler))
}

func NewTestZeroEventHubAPI() *TestZeroEventHubAPI {
	api := TestZeroEventHubAPI{partitions: map[int][]TestEvent{}}
	partition0 := make([]TestEvent, 10000)
	partition1 := make([]TestEvent, 10000)
	for i := 0; i < 10000; i++ {
		partition0[i] = TestEvent{
			ID:      fmt.Sprintf("00000000-0000-0000-0000-%012x", i),
			Version: 0,
			Cursor:  i,
		}
		partition1[i] = TestEvent{
			ID:      fmt.Sprintf("11111111-0000-0000-0000-%012x", i),
			Version: 0,
			Cursor:  i,
		}
	}
	api.partitions[0] = partition0
	api.partitions[1] = partition1
	return &api
}

func (t TestZeroEventHubAPI) GetName() string {
	return "TestZeroEventHubAPI"
}

func (t TestZeroEventHubAPI) GetPartitionCount() int {
	return 2
}

func (t TestZeroEventHubAPI) FetchEvents(ctx context.Context, cursors []Cursor, pageSizeHint int, r EventReceiver, headers ...string) error {
	if pageSizeHint == DefaultPageSize {
		pageSizeHint = 100
	}
	for _, cursor := range cursors {
		partition, ok := t.partitions[cursor.PartitionID]
		if !ok {
			return ErrPartitionDoesntExist
		}
		var err error
		var lastProcessedCursor int
		switch cursor.Cursor {
		case FirstCursor:
			lastProcessedCursor = -100
		case LastCursor:
			lastProcessedCursor = len(partition) - 2
		// Mock responses: set the cursor to one of the following values to get a mocked response.
		case cursorReturn500:
			return err500
		case cursorReturn504:
			return err504
		default:
			lastProcessedCursor, err = strconv.Atoi(cursor.Cursor)
			if err != nil {
				return err
			}
		}
		eventsProcessed := 0
		h := make(map[string]string, 1)
		for _, header := range headers {
			if header == "content-type" {
				h["content-type"] = "application/json"
				break
			}
			if header == All {
				h["content-type"] = "application/json"
				h["foo"] = "bar"
				break
			}
		}
		for _, event := range partition {
			if event.Cursor > lastProcessedCursor {
				if err := r.Event(cursor.PartitionID, h, mustMarshalJson(partition[event.Cursor])); err != nil {
					return err
				}
				if err := r.Checkpoint(cursor.PartitionID, fmt.Sprintf("%d", event.Cursor)); err != nil {
					return err
				}
				lastProcessedCursor = event.Cursor
				eventsProcessed++
			}
			if eventsProcessed == pageSizeHint {
				break
			}
		}
	}
	return nil
}

func mustMarshalJson(e any) json.RawMessage {
	result, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return result
}
