package zeroeventhub

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// EventPublisher is a generic-based interface that has to be implemented on a server side.
type EventPublisher interface {
	// GetName should return the name of the EventPublisher (used in logging).
	GetName() string
	// GetPartitionCount should return amount of partitions available at this EventPublisher (used in a handshake).
	GetPartitionCount() int

	EventFetcher
}

// HTTPHandlers wraps eventPublisher to provide what you need for HTTP server, implementing
// both protocols version 1 and 2. It is required that you install two handlers: DiscoveryHandler
// and FetchEventsHandler. The first one is put on an endpoint of your own choosing; the
// latter should be installed at `/events` relative to the first one.
type HTTPHandlers struct {
	EventPublisher    EventPublisher
	LoggerFromRequest func(*http.Request) logrus.FieldLogger
}

// DiscoveryHandler should be handling GET requests on the main URL of your FeedAPI
// endpoint. Note: It will also serve events in v1 of the protocol.
func (h HTTPHandlers) DiscoveryHandler(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if query.Has("n") {
		// version 1 of the protocol, "ZeroEventHub"
		h.ZeroEventHubV1Handler(writer, request)
		return
	}

	writer.WriteHeader(http.StatusNotImplemented)
}

func (h HTTPHandlers) ZeroEventHubV1Handler(writer http.ResponseWriter, request *http.Request) {
	logger := h.LoggerFromRequest(request)
	query := request.URL.Query()
	if !query.Has("n") {
		http.Error(writer, ErrHandshakePartitionCountMissing.Error(), ErrHandshakePartitionCountMissing.Status())
		return
	}
	if n, err := strconv.Atoi(query.Get("n")); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	} else {
		if n != h.EventPublisher.GetPartitionCount() {
			http.Error(writer, ErrHandshakePartitionCountMismatch.Error(), ErrHandshakePartitionCountMismatch.Status())
			return
		}
	}
	pageSizeHint := DefaultPageSize
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
	cursors := parseCursors(h.EventPublisher.GetPartitionCount(), query)
	if len(cursors) == 0 {
		http.Error(writer, ErrCursorsMissing.message, http.StatusBadRequest)
		return
	} else if len(cursors) > 1 {
		// we used to support multiple cursors in the v1 protocol. This feature went unused
		// and was then deprecated; but that is the reason for the strange signature.
		http.Error(writer, "support for multiple cursors in the same request has been removed", http.StatusBadRequest)
		return
	}
	partitionID := cursors[0].PartitionID
	cursor := cursors[0].Cursor

	fields := logger.
		WithField("event", h.EventPublisher.GetName()).
		WithField("PartitionCount", h.EventPublisher.GetPartitionCount()).
		WithField("partitionID", partitionID).
		WithField("cursors", cursor).
		WithField("PageSizeHint", pageSizeHint).
		WithField("Headers", headers)
	fields.Info()
	serializer := NewNDJSONEventSerializer(writer)
	err := h.EventPublisher.FetchEvents(request.Context(), "", partitionID, cursor, serializer, Options{
		PageSizeHint: pageSizeHint,
	})
	if err != nil {
		logger.WithField("event", h.EventPublisher.GetName()+".fetch_events_error").WithError(err).Info()
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
		return
	}
}

func (h HTTPHandlers) EventsHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func parseCursors(partitionCount int, query url.Values) (cursors []Cursor) {
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
	return
}
