package zeroeventhub

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"strconv"
)

// EventPublisher is a generic-based interface that has to be implemented on a server side.
type EventPublisher interface {
	// GetName should return the name of the EventPublisher (used in logging).
	GetName() string
	GetFeedInfo() FeedInfo

	EventFetcher
}

// HTTPHandlers wraps eventPublisher to provide what you need for HTTP server, implementing
// both protocols version 1 and 2. It is required that you install two handlers: DiscoveryHandler
// and FetchEventsHandler. The first one is put on an endpoint of your own choosing; the
// latter should be installed at `/events` relative to the first one.
type HTTPHandlers struct {
	eventPublisher    EventPublisher
	loggerFromRequest func(*http.Request) logrus.FieldLogger

	feedInfo       FeedInfo
	partitionsById map[int]Partition
}

func NewHTTPHandlers(publisher EventPublisher, loggerFromRequest func(*http.Request) logrus.FieldLogger) HTTPHandlers {
	feedInfo := publisher.GetFeedInfo()
	partitionsById := make(map[int]Partition)
	for _, p := range feedInfo.Partitions {
		partitionsById[p.Id] = p
	}

	return HTTPHandlers{
		eventPublisher:    publisher,
		loggerFromRequest: loggerFromRequest,

		feedInfo:       feedInfo,
		partitionsById: partitionsById,
	}
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

	// We are on version 2
	encodedInfo, err := json.Marshal(h.eventPublisher.GetFeedInfo())
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(encodedInfo)
}

func (h HTTPHandlers) EventsHandler(writer http.ResponseWriter, request *http.Request) {
	partitionCount := len(h.eventPublisher.GetFeedInfo().Partitions)
	logger := h.loggerFromRequest(request)

	query := request.URL.Query()
	if !query.Has("token") || query.Get("token") != h.feedInfo.Token {
		http.Error(writer, ErrIllegalToken.Error(), ErrIllegalToken.Status())
		return
	}

	var partitionId int
	var err error
	if partitionId, err = strconv.Atoi(query.Get("partition")); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	} else if _, ok := h.partitionsById[partitionId]; !ok {
		http.Error(writer, ErrPartitionDoesntExist.Error(), ErrPartitionDoesntExist.Status())
		return
	}

	pageSizeHint := DefaultPageSize
	if query.Has("pagesizehint") {
		if x, err := strconv.Atoi(query.Get("pagesizehint")); err != nil {
			http.Error(writer, "pagesizehint not an integer", http.StatusBadRequest)
			return
		} else {
			pageSizeHint = x
		}
	}

	if !query.Has("cursor") {
		http.Error(writer, "no cursor argument", http.StatusBadRequest)
	}
	cursor := query.Get("cursor")

	fields := logger.
		WithField("event", h.eventPublisher.GetName()).
		WithField("PartitionCount", partitionCount).
		WithField("partitionID", partitionId).
		WithField("cursors", cursor).
		WithField("PageSizeHint", pageSizeHint)
	fields.Info()
	serializer := NewNDJSONEventSerializer(writer)
	err = h.eventPublisher.FetchEvents(request.Context(), "", partitionId, cursor, serializer, Options{
		PageSizeHint: pageSizeHint,
	})
	if err != nil {
		logger.WithField("publisherName", h.eventPublisher.GetName()).
			WithField("event", "feedapi.server.fetch_events_error").WithError(err).Info()
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
		return
	}
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
