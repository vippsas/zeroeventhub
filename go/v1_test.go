package zeroeventhub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	hookstest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func createZehClientWithPartitionCount(server *httptest.Server, partitionCount int) Client {
	return NewClient(fmt.Sprintf("%s/testfeed", server.URL), partitionCount)
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

func TestAPI_V1(t *testing.T) {
	server := Server(NewTestZeroEventHubAPI())
	tests := []struct {
		name string

		partitionCount int
		pageSizeHint   int
		partitionID    int
		cursor         string

		expectedEvents      int
		expectedErrorString string
	}{
		{
			name:                "wrong partition count",
			partitionCount:      1,
			partitionID:         0,
			cursor:              "qwerty",
			expectedErrorString: "unexpected response body: handshake error: partition count mismatch\n",
		},
		{
			name:                "wrong cursor",
			partitionCount:      2,
			partitionID:         0,
			cursor:              "qwerty",
			expectedErrorString: "unexpected response body: Internal server error\n",
		},
		{
			name:           "out of range cursor",
			partitionCount: 2,
			partitionID:    0,
			cursor:         "20000",
			expectedEvents: 0,
		},
		{
			name:           "_first special cursor",
			partitionCount: 2,
			partitionID:    0,
			cursor:         FirstCursor,
			expectedEvents: 100,
		},
		{
			name:           "_last special cursor",
			partitionCount: 2,
			partitionID:    0,
			cursor:         LastCursor,
			expectedEvents: 1,
		},
		{
			name:           "pagesizehint 10000, full page",
			partitionCount: 2,
			pageSizeHint:   10000,
			partitionID:    0,
			cursor:         FirstCursor,
			expectedEvents: 10000,
		},
		{
			name:           "pagesizehint 10000, half page",
			partitionCount: 2,
			pageSizeHint:   10000,
			partitionID:    0,
			cursor:         "4999",
			expectedEvents: 5000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var client EventFetcher = createZehClientWithPartitionCount(server, test.partitionCount)
			var page EventPageSingleType[TestEvent]
			err := client.FetchEvents(context.Background(), V1Token, test.partitionID, test.cursor, &page, Options{
				PageSizeHint: test.pageSizeHint,
			})
			if err == nil {
				require.Equal(t, test.expectedEvents, len(page.Events))
			} else {
				require.Equal(t, test.expectedErrorString, err.Error())
			}
		})
	}
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
	server := Server(NewTestZeroEventHubAPI())
	loggingClient := server.Client()
	loggingRoundTripper := loggingRoundTripper{actualRoundTripper: server.Client().Transport}
	loggingClient.Transport = &loggingRoundTripper
	client := createZehClient(server).WithHttpClient(loggingClient)
	var page EventPageSingleType[TestEvent]
	err := client.FetchEvents(context.Background(), V1Token, 0, "9998", &page, Options{})
	require.NoError(t, err)
	require.Equal(t, `{"data":{"ID":"00000000-0000-0000-0000-00000000270f","Version":0,"Cursor":9999}}
{"cursor":"9999"}
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
	err := client.FetchEvents(context.Background(), V1Token, 0, "9999", &page1, Options{})
	require.NoError(t, err)

	require.Equal(t, []TestEvent{
		{
			ID:     "414e0173-c3e5-4935-a59d-15e4d3c462e0",
			Cursor: 9999,
		},
		{
			ID:     "a79e2138-64df-4493-8ca5-bc84f6bb31c1",
			Cursor: 9999,
		},
	}, page1.Events)

	var page2 EventPageSingleType[TestEvent]
	client = NewClient(server.URL+"/withoutNewLineAtTheEnd/feed/v1", 2).WithHttpClient(server.Client())
	err = client.FetchEvents(context.Background(), V1Token, 0, "9999", &page2, Options{})
	require.NoError(t, err)
	require.Equal(t, page1, page2)
}

func TestRequestProcessor(t *testing.T) {
	server := Server(NewTestZeroEventHubAPI())
	loggingClient := server.Client()
	loggingRoundTripper := loggingRoundTripper{actualRoundTripper: server.Client().Transport}
	loggingClient.Transport = &loggingRoundTripper
	client := createZehClient(server).WithHttpClient(loggingClient).WithRequestProcessor(func(r *http.Request) error {
		r.Header.Set("content-type", "application/json")
		return nil
	})
	var page EventPageSingleType[TestEvent]
	err := client.FetchEvents(context.Background(), V1Token, 0, LastCursor, &page, Options{})
	require.NoError(t, err)
	require.NotNil(t, loggingRoundTripper.requestHeaders)
	require.Equal(t, "application/json", loggingRoundTripper.requestHeaders.Get("content-type"))
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
	handlerFunc := func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/testfeed" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}

		query := request.URL.Query()
		cursors := parseCursors(len(api.GetFeedInfo().Partitions), query)
		if len(cursors) == 0 {
			http.Error(writer, ErrCursorsMissing.message, http.StatusBadRequest)
			return
		}
		if len(cursors) > 1 {
			http.Error(writer, "too many cursors (deprecated)", http.StatusBadRequest)
			return
		}

		serializer := NewNDJSONEventSerializer(writer)
		err := api.FetchEvents(request.Context(), "", cursors[0].PartitionID, cursors[0].Cursor, serializer, Options{})
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
	}
	return http.HandlerFunc(handlerFunc)
}

func TestMockResponses(t *testing.T) {
	log := logrus.New()
	h := hookstest.NewLocal(log)
	logrus.AddHook(h)

	server := httptest.NewServer(MockHandler(nil, NewTestZeroEventHubAPI()))
	client := createZehClient(server)
	var page EventPageSingleType[TestEvent]

	err := client.FetchEvents(context.Background(), V1Token, 0, cursorReturn500, &page, Options{})
	require.EqualError(t, err, "unexpected response body: error when fetching events\n")
	err = client.FetchEvents(context.Background(), V1Token, 0, cursorReturn504, &page, Options{})
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
