package zeroeventhub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

// EventReceiver is an interface describing an abstraction for handling either events, or checkpoints.
// Checkpoint in this context is basically a cursor.
type EventReceiver interface {
	// Event method processes actual events.
	Event(partitionID int, headers map[string]string, Data json.RawMessage) error
	// Checkpoint method processes cursors.
	Checkpoint(partitionID int, cursor string) error
}

type Options struct {
	PageSizeHint int
	Wait         time.Duration
	Stream       time.Duration
	Headers      []string
}

func (o Options) AllHeaders() Options {
	o.Headers = []string{All}
	return o
}

// EventFetcher is a generic-based interface providing a contract for fetching events: both for the server side and
// client side implementations.
type EventFetcher interface {
	// FetchEvents method accepts array of Cursor's along with an optional page size hint and an EventReceiver.
	// Pass pageSizeHint = 0 for having the server choose a default / no hint.
	// Optional `headers` argument specifies headers to be returned, or none, if it's absent.
	FetchEvents(ctx context.Context, cursors []Cursor, receiver EventReceiver, options Options) error
}

// API is a generic-based interface that has to be implemented on a server side.
type API interface {
	// GetName should return the name of the API (used in logging).
	GetName() string
	// GetPartitionCount should return amount of partitions available at this API (used in a handshake).
	GetPartitionCount() int

	EventFetcher
}

// NDJSONEventSerializer implements EventReceiver by emitting Newline-Delimited-JSON to a writer.
type NDJSONEventSerializer struct {
	encoder *json.Encoder
	writer  io.Writer
	flusher http.Flusher
}

type NoopFlusher struct{}

func (n NoopFlusher) Flush() {
}

func NewNDJSONEventSerializer(writer io.Writer, flush bool) *NDJSONEventSerializer {
	var flusher http.Flusher
	if flush {
		flusher, _ = writer.(http.Flusher)
	}
	if flusher == nil {
		flusher = NoopFlusher{}
	}
	return &NDJSONEventSerializer{
		encoder: json.NewEncoder(writer),
		writer:  writer,
		flusher: flusher,
	}
}

func (s NDJSONEventSerializer) writeNdJsonLine(item interface{}) error {
	if err := s.encoder.Encode(item); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
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

// HandlerWithoutRoute wraps API in a http.Handler that implements the
// ZeroEventHub HTTP protocol. The path/method is not checked. Use this method
// to plug a handler into your own service routing.
func HandlerWithoutRoute(api API, getLogger func(request *http.Request) logrus.FieldLogger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		logger := getLogger(request)
		query := request.URL.Query()
		if !query.Has("n") {
			http.Error(writer, ErrHandshakePartitionCountMissing.Error(), ErrHandshakePartitionCountMissing.Status())
			return
		}

		if n, err := strconv.Atoi(query.Get("n")); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		} else {
			if n != api.GetPartitionCount() {
				http.Error(writer, ErrHandshakePartitionCountMismatch.Error(), ErrHandshakePartitionCountMismatch.Status())
				return
			}
		}

		var options Options
		if query.Has("pagesizehint") {
			if x, err := strconv.Atoi(query.Get("pagesizehint")); err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			} else {
				options.PageSizeHint = x
			}
		}

		parseMilliseconds := func(key string) (time.Duration, error) {
			if query.Has(key) {
				intResult, err := strconv.Atoi(query.Get(key))
				if err != nil {
					return 0, err
				}
				return time.Duration(intResult) * time.Millisecond, nil
			} else {
				return 0, nil
			}
		}

		var err error

		options.Wait, err = parseMilliseconds("wait")
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}

		options.Stream, err = parseMilliseconds("stream")
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}

		if query.Has("headers") {
			options.Headers = strings.Split(strings.TrimSuffix(query.Get("headers"), ","), ",")
		}
		cursors, err := parseCursors(api.GetPartitionCount(), query)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		fields := logger.
			WithField("event", api.GetName()).
			WithField("PartitionCount", api.GetPartitionCount()).
			WithField("Cursors", cursors).
			WithField("PageSizeHint", options.PageSizeHint).
			WithField("Headers", options.Headers).
			WithField("Wait", options.Wait.Milliseconds()).
			WithField("Stream", options.Stream)
		fields.Info()
		serializer := NewNDJSONEventSerializer(writer, options.Stream != 0)
		err = api.FetchEvents(request.Context(), cursors, serializer, options)
		if err != nil {
			logger.WithField("event", api.GetName()+".fetch_events_error").WithError(err).Info()
			http.Error(writer, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

// Handler wraps API in a http.Handler that checks the path and method (GET)
// in addition to serving the feed like HandlerWithoutRoute does.
// Note: `logger` is also hardcoded if you use this function; use HandlerWithoutRoute
// directly to also be able to configure the logger per request to e.g.
// include request IDs in log output.
func Handler(path string, logger logrus.FieldLogger, api API) http.Handler {
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	getLogger := func(*http.Request) logrus.FieldLogger {
		return logger
	}
	router := mux.NewRouter()
	router.Methods(http.MethodGet).
		Path(path).
		Handler(HandlerWithoutRoute(api, getLogger))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		router.ServeHTTP(writer, request)
	})
}

func parseCursors(partitionCount int, query url.Values) (cursors []Cursor, err error) {
	for i := 0; i < partitionCount; i++ {
		partition := fmt.Sprintf("cursor%d", i)
		if !query.Has(partition) {
			continue
		}
		cursors = append(cursors, Cursor{
			PartitionID: i,
			Cursor:      query.Get(partition),
		})
	}
	if len(cursors) == 0 {
		err = ErrCursorsMissing
	}
	return
}

// Client struct is a generic-based client-side implementation of the EventFetcher interface.
type Client struct {
	httpClient       *http.Client
	requestProcessor func(r *http.Request) error
	logger           logrus.FieldLogger
	url              string
	partitionCount   int
}

var _ EventFetcher = &Client{}

// NewClient is a constructor for the Client.
func NewClient(url string, partitionCount int) Client {
	return Client{
		httpClient: http.DefaultClient,
		requestProcessor: func(r *http.Request) error {
			return nil
		},
		logger:         logrus.StandardLogger(),
		url:            url,
		partitionCount: partitionCount,
	}
}

// WithHttpClient is a Client method for providing custom HTTP client.
func (c Client) WithHttpClient(httpClient *http.Client) (r Client) {
	r = c
	r.httpClient = httpClient
	return
}

func (c Client) WithRequestProcessor(requestProcessor func(r *http.Request) error) (r Client) {
	r = c
	r.requestProcessor = requestProcessor
	return
}

// WithLogger is a Client method for providing custom logger.
func (c Client) WithLogger(logger logrus.FieldLogger) (r Client) {
	r = c
	r.logger = logger
	return
}

type checkpointOrEvent struct {
	PartitionId int `json:"partition"`
	// either this is set:
	Cursor string `json:"cursor"`
	// OR, these are set:
	Headers map[string]string `json:"headers"`
	Data    json.RawMessage   `json:"data"`
}

// FetchEvents is a client-side implementation that queries the server and properly deserializes received data.
func (c Client) FetchEvents(ctx context.Context, cursors []Cursor, r EventReceiver, options Options) error {
	if len(cursors) == 0 {
		return ErrCursorsMissing
	}

	req, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}

	req = req.WithContext(ctx)

	q := req.URL.Query()
	q.Add("n", fmt.Sprintf("%d", c.partitionCount))
	if options.PageSizeHint != DefaultPageSize {
		q.Add("pagesizehint", fmt.Sprintf("%d", options.PageSizeHint))
	}
	for _, cursor := range cursors {
		q.Add(fmt.Sprintf("cursor%d", cursor.PartitionID), fmt.Sprintf("%s", cursor.Cursor))
	}
	if len(options.Headers) != 0 {
		q.Add("headers", strings.Join(options.Headers, ","))
	}
	req.URL.RawQuery = q.Encode()

	if options.Stream != 0 {
		q.Add("stream", fmt.Sprintf("%d", options.Stream.Milliseconds()))
	}

	if options.Wait != 0 {
		q.Add("wait", fmt.Sprintf("%d", options.Wait.Milliseconds()))
	}

	if err := c.requestProcessor(req); err != nil {
		return err
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(res.Body)

	if res.StatusCode/100 != 2 {
		log := c.logger.WithFields(logrus.Fields{
			"responseCode": strconv.Itoa(res.StatusCode),
			"requestUrl":   req.URL.String(),
		}).WithContext(ctx)
		if all, err := io.ReadAll(res.Body); err != nil {
			log.WithField("event", "zeroeventhub.res_body_read_error").WithError(err).Error()
			return err
		} else {
			if string(all) == "\n" || string(all) == "" {
				err = errors.Errorf("empty response body")
			} else {
				err = errors.Errorf("unexpected response body: %s", string(all))
			}
			log.WithField("event", "zeroeventhub.unexpected_response_body").WithError(err).Error()
			return err
		}
	}

	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		// we only partially parse at this point, as "data" is json.RawMessage
		var parsedLine checkpointOrEvent
		if err := json.Unmarshal(line, &parsedLine); err != nil {
			return err
		}
		if parsedLine.Cursor != "" {
			// checkpoint
			if err := r.Checkpoint(parsedLine.PartitionId, parsedLine.Cursor); err != nil {
				return err
			}

		} else {
			// event
			if err := r.Event(parsedLine.PartitionId, parsedLine.Headers, parsedLine.Data); err != nil {
				return err
			}
		}
	}

	return nil
}
