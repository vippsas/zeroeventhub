package zeroeventhub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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

// EventFetcher is a generic-based interface providing a contract for fetching events: both for the server side and
// client side implementations.
type EventFetcher interface {
	// FetchEvents method accepts array of Cursor's along with an optional page size hint and an EventReceiver.
	// Pass pageSizeHint = 0 for having the server choose a default / no hint.
	// Optional `headers` argument specifies headers to be returned, or none, if it's absent.
	FetchEvents(ctx context.Context, cursors []Cursor, pageSizeHint int, receiver EventReceiver, headers ...string) error
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

// Handler wraps API in a http.Handler.
func Handler(logger logrus.FieldLogger, api API) http.Handler {
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	router := mux.NewRouter()
	router.Methods(http.MethodGet).
		Path("/feed/v1").
		HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
			var pageSizeHint int
			if query.Has("pagesizehint") {
				if x, err := strconv.Atoi(query.Get("pagesizehint")); err != nil {
					http.Error(writer, err.Error(), http.StatusBadRequest)
					return
				} else {
					pageSizeHint = x
				}
			}
			var headers []string
			if query.Has("headers") {
				headers = strings.Split(strings.TrimSuffix(query.Get("headers"), ","), ",")
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
				WithField("PageSizeHint", pageSizeHint).
				WithField("Headers", headers)
			fields.Info()
			serializer := NewNDJSONEventSerializer(writer)
			err = api.FetchEvents(request.Context(), cursors, pageSizeHint, serializer, headers...)
			if err != nil {
				logger.WithField("event", api.GetName()+".fetch_events_error").WithError(err).Info()
				http.Error(writer, "Internal server error", http.StatusInternalServerError)
				return
			}
		})
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
func (c Client) FetchEvents(ctx context.Context, cursors []Cursor, pageSizeHint int, r EventReceiver, headers ...string) error {
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
	if pageSizeHint != DefaultPageSize {
		q.Add("pagesizehint", fmt.Sprintf("%d", pageSizeHint))
	}
	for _, cursor := range cursors {
		q.Add(fmt.Sprintf("cursor%d", cursor.PartitionID), fmt.Sprintf("%s", cursor.Cursor))
	}
	if len(headers) != 0 {
		q.Add("headers", strings.Join(headers, ","))
	}
	req.URL.RawQuery = q.Encode()

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
