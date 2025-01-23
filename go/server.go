package zeroeventhub

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// EventPublisher is a generic-based interface that has to be implemented on a server side.
type EventPublisher interface {
	// GetName should return the name of the EventPublisher (used in logging).
	GetName() string
	// GetPartitionCount should return amount of partitions available at this EventPublisher (used in a handshake).
	GetPartitionCount() int

	EventFetcher
}

// HandlerWithoutRoute wraps EventPublisher in a http.Handler that implements the
// ZeroEventHub HTTP protocol. The path/method is not checked. Use this method
// to plug a handler into your own service routing.
func HandlerWithoutRoute(api EventPublisher, getLogger func(request *http.Request) logrus.FieldLogger) http.Handler {
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
		fields.Debug()
		serializer := NewNDJSONEventSerializer(writer)
		err = api.FetchEvents(request.Context(), cursors, pageSizeHint, serializer, headers...)
		if err != nil {
			logger.WithField("event", api.GetName()+".fetch_events_error").WithError(err).Info()
			http.Error(writer, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

// Handler wraps EventPublisher in a http.Handler that checks the path and method (GET)
// in addition to serving the feed like HandlerWithoutRoute does.
// Note: `logger` is also hardcoded if you use this function; use HandlerWithoutRoute
// directly to also be able to configure the logger per request to e.g.
// include request IDs in log output.
func Handler(path string, logger logrus.FieldLogger, api EventPublisher) http.Handler {
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
