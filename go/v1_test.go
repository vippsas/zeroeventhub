package zeroeventhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	hookstest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func mustMarshalJson(e any) json.RawMessage {
	result, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return result
}

func createZehClientWithPartitionCount(server *httptest.Server, partitionCount int) Client {
	return NewClient(fmt.Sprintf("%s/feed/v1", server.URL), partitionCount)
}

func createZehClient(server *httptest.Server) Client {
	return createZehClientWithPartitionCount(server, 2)
}

type TestEvent struct {
	ID      string
	Version int
	Cursor  int
}

type TestZeroEventHubAPI struct {
	partitions map[int][]TestEvent
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

func TestAPI(t *testing.T) {
	server := httptest.NewServer(Handler("/feed/v1", nil, NewTestZeroEventHubAPI()))
	tests := []struct {
		name string

		partitionCount int
		pageSizeHint   int
		cursors        []Cursor

		expectedEvents      int
		expectedErrorString string
	}{
		{
			name:           "wrong partition count",
			partitionCount: 1,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      "qwerty",
			}},
			expectedErrorString: "unexpected response body: handshake error: partition count mismatch\n",
		},
		{
			name:           "wrong cursor",
			partitionCount: 2,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      "qwerty",
			}},
			expectedErrorString: "unexpected response body: Internal server error\n",
		},
		{
			name:           "out of range cursor",
			partitionCount: 2,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      "20000",
			}},
			expectedEvents: 0,
		},
		{
			name:           "_first special cursor",
			partitionCount: 2,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      FirstCursor,
			}},
			expectedEvents: 100,
		},
		{
			name:           "_last special cursor",
			partitionCount: 2,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      LastCursor,
			}},
			expectedEvents: 1,
		},
		{
			name:           "both special cursors",
			partitionCount: 2,
			cursors: []Cursor{
				{
					PartitionID: 0,
					Cursor:      FirstCursor,
				},
				{
					PartitionID: 1,
					Cursor:      LastCursor,
				},
			},
			expectedEvents: 101,
		},
		{
			name:           "regular cursors",
			partitionCount: 2,
			cursors: []Cursor{
				{
					PartitionID: 0,
					Cursor:      "123",
				},
				{
					PartitionID: 1,
					Cursor:      "456",
				},
			},
			expectedEvents: 200,
		},
		{
			name:           "pagesizehint 10000, full page",
			partitionCount: 2,
			pageSizeHint:   10000,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      FirstCursor,
			}},
			expectedEvents: 10000,
		},
		{
			name:           "pagesizehint 10000, half page",
			partitionCount: 2,
			pageSizeHint:   10000,
			cursors: []Cursor{{
				PartitionID: 0,
				Cursor:      "4999",
			}},
			expectedEvents: 5000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var client EventFetcher = createZehClientWithPartitionCount(server, test.partitionCount)
			var page EventPageSingleType[TestEvent]
			err := client.FetchEvents(context.Background(), test.cursors, test.pageSizeHint, &page)
			if err == nil {
				require.Equal(t, test.expectedEvents, len(page.Events))
			} else {
				require.Equal(t, test.expectedErrorString, err.Error())
			}
		})
	}
}

func BenchmarkFeed(b *testing.B) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	server := httptest.NewServer(Handler("/feed/v1", logger, NewTestZeroEventHubAPI()))
	client := createZehClient(server).WithLogger(logger)
	var page EventPageSingleType[TestEvent]
	err := client.FetchEvents(context.Background(), []Cursor{
		{
			PartitionID: 0,
			Cursor:      FirstCursor,
		},
		{
			PartitionID: 1,
			Cursor:      FirstCursor,
		},
	}, 1, &page)
	require.NoError(b, err)
}

type loggingRoundTripper struct {
	actualRoundTripper http.RoundTripper
	requestHeaders     http.Header
	response           string
}

func (l *loggingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	l.requestHeaders = request.Header
	response, err := l.actualRoundTripper.RoundTrip(request)
	all, err := io.ReadAll(response.Body)
	if err != nil {
		return response, err
	}
	l.response = string(all)
	return response, err
}

func TestJSON(t *testing.T) {
	server := httptest.NewServer(Handler("/feed/v1", nil, NewTestZeroEventHubAPI()))
	loggingClient := server.Client()
	loggingRoundTripper := loggingRoundTripper{actualRoundTripper: server.Client().Transport}
	loggingClient.Transport = &loggingRoundTripper
	client := createZehClient(server).WithHttpClient(loggingClient)
	var page EventPageSingleType[TestEvent]
	err := client.FetchEvents(context.Background(), []Cursor{
		{
			PartitionID: 0,
			Cursor:      "9998",
		},
		{
			PartitionID: 1,
			Cursor:      "9998",
		},
	}, DefaultPageSize, &page)
	require.NoError(t, err)
	require.Equal(t, `{"partition":0,"data":{"ID":"00000000-0000-0000-0000-00000000270f","Version":0,"Cursor":9999}}
{"partition":0,"cursor":"9999"}
{"partition":1,"data":{"ID":"11111111-0000-0000-0000-00000000270f","Version":0,"Cursor":9999}}
{"partition":1,"cursor":"9999"}
`, loggingRoundTripper.response)
	fmt.Print(loggingRoundTripper.response)
}

func TestNewLines(t *testing.T) {
	const payloadWithoutTrailingNewline = "" +
		`{"partition":0,"headers":{"h1": "v1"},"data":{"ID":"414e0173-c3e5-4935-a59d-15e4d3c462e0","Version":0,"Cursor":9999}}` + "\n" +
		`{"partition":0,"cursor": "9999"}` + "\n" +
		`{"partition":1,"headers":{"h2": "v2"},"data":{"ID":"a79e2138-64df-4493-8ca5-bc84f6bb31c1","Version":0,"Cursor":9999}}` + "\n" +
		`{"partition":1,"cursor": "9999"}` + "\n"

	router := mux.NewRouter()
	router.Methods(http.MethodGet).
		Path("/withNewLineAtTheEnd/feed/v1").
		HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(payloadWithoutTrailingNewline + "\n"))

		})
	router.Methods(http.MethodGet).
		Path("/withoutNewLineAtTheEnd/feed/v1").
		HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(payloadWithoutTrailingNewline))
		})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		router.ServeHTTP(writer, request)
	}))

	var page1 EventPageSingleType[TestEvent]
	client := NewClient(server.URL+"/withNewLineAtTheEnd/feed/v1", 2).WithHttpClient(server.Client())
	err := client.FetchEvents(context.Background(), []Cursor{
		{
			PartitionID: 0,
			Cursor:      "9999",
		},
		{
			PartitionID: 1,
			Cursor:      "9999",
		},
	}, DefaultPageSize, &page1)
	require.NoError(t, err)

	require.Equal(t, []TypedEnvelope[TestEvent]{
		{
			Headers: map[string]string{"h1": "v1"},
			Data: TestEvent{
				ID:     "414e0173-c3e5-4935-a59d-15e4d3c462e0",
				Cursor: 9999,
			},
		},
		{
			PartitionID: 1,
			Headers:     map[string]string{"h2": "v2"},
			Data: TestEvent{
				ID:     "a79e2138-64df-4493-8ca5-bc84f6bb31c1",
				Cursor: 9999,
			},
		},
	}, page1.Events)

	var page2 EventPageSingleType[TestEvent]
	client = NewClient(server.URL+"/withoutNewLineAtTheEnd/feed/v1", 2).WithHttpClient(server.Client())
	err = client.FetchEvents(context.Background(), []Cursor{
		{
			PartitionID: 0,
			Cursor:      "9999",
		},
		{
			PartitionID: 1,
			Cursor:      "9999",
		},
	}, DefaultPageSize, &page2)
	require.NoError(t, err)
	require.Equal(t, page1, page2)
}

func TestRequestProcessor(t *testing.T) {
	server := httptest.NewServer(Handler("/feed/v1", nil, NewTestZeroEventHubAPI()))
	loggingClient := server.Client()
	loggingRoundTripper := loggingRoundTripper{actualRoundTripper: server.Client().Transport}
	loggingClient.Transport = &loggingRoundTripper
	client := createZehClient(server).WithHttpClient(loggingClient).WithRequestProcessor(func(r *http.Request) error {
		r.Header.Set("content-type", "application/json")
		return nil
	})
	var page EventPageSingleType[TestEvent]
	err := client.FetchEvents(context.Background(), []Cursor{{Cursor: LastCursor}}, DefaultPageSize, &page)
	require.NoError(t, err)
	require.NotNil(t, loggingRoundTripper.requestHeaders)
	require.Equal(t, "application/json", loggingRoundTripper.requestHeaders.Get("content-type"))
}

func TestEnvelopeHeaders(t *testing.T) {
	server := httptest.NewServer(Handler("/feed/v1", nil, NewTestZeroEventHubAPI()))
	client := createZehClient(server)
	var page EventPageSingleType[TestEvent]
	err := client.FetchEvents(context.Background(), []Cursor{{Cursor: LastCursor}}, DefaultPageSize, &page, "123")
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Len(t, page.Cursors, 1)
	require.Empty(t, page.Events[0].Headers)
	page = EventPageSingleType[TestEvent]{}
	err = client.FetchEvents(context.Background(), []Cursor{{Cursor: LastCursor}}, DefaultPageSize, &page, "content-type")
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Len(t, page.Cursors, 1)
	require.Len(t, page.Events[0].Headers, 1)
	require.Equal(t, "application/json", page.Events[0].Headers["content-type"])
	page = EventPageSingleType[TestEvent]{}
	err = client.FetchEvents(context.Background(), []Cursor{{Cursor: LastCursor}}, DefaultPageSize, &page, All)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Len(t, page.Cursors, 1)
	require.Len(t, page.Events[0].Headers, 2)
	require.Equal(t, "application/json", page.Events[0].Headers["content-type"])
	require.Equal(t, "bar", page.Events[0].Headers["foo"])
}

// Variables for mocking responses
var err500 = errors.New("error when fetching events")
var err504 = errors.New("") // The response body is supposed to be blank in this case.

const (
	cursorReturn500 = "returnHttp500"
	cursorReturn504 = "returnHttp504"
)

func MockHandler(logger logrus.FieldLogger, api EventPublisher) http.Handler {
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	router := mux.NewRouter()
	router.Methods(http.MethodGet).
		Path("/feed/v1").
		HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			query := request.URL.Query()
			cursors, err := parseCursors(api.GetPartitionCount(), query)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}

			serializer := NewNDJSONEventSerializer(writer)
			err = api.FetchEvents(request.Context(), cursors, 10, serializer, All)
			switch err {
			case err500:
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			case err504:
				http.Error(writer, err.Error(), http.StatusGatewayTimeout)
				return
			default:
				// Proceed
			}
		})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		router.ServeHTTP(writer, request)
	})
}

func TestMockResponses(t *testing.T) {
	log := logrus.New()
	h := hookstest.NewLocal(log)
	logrus.AddHook(h)

	server := httptest.NewServer(MockHandler(nil, NewTestZeroEventHubAPI()))
	client := createZehClient(server)
	var page EventPageSingleType[TestEvent]

	err := client.FetchEvents(context.Background(), []Cursor{{Cursor: cursorReturn500}}, DefaultPageSize, &page, All)
	require.EqualError(t, err, "unexpected response body: error when fetching events\n")
	err = client.FetchEvents(context.Background(), []Cursor{{Cursor: cursorReturn504}}, DefaultPageSize, &page, All)
	require.EqualError(t, err, "empty response body")

	// Checking logged entries
	http500logged := false
	http504logged := false
	for _, e := range h.AllEntries() {
		if e.Data["responseCode"] == "500" {
			http500logged = true
		}
		if e.Data["responseCode"] == "504" {
			http504logged = true
		}
	}

	assert.True(t, http500logged)
	assert.True(t, http504logged)
}
