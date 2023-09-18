package zeroeventhub

import (
	"net/http"
	"strconv"
	"strings"
)

func (h HTTPHandlers) ZeroEventHubV1Handler(writer http.ResponseWriter, request *http.Request) {
	partitionCount := len(h.eventPublisher.GetFeedInfo().Partitions)
	logger := h.loggerFromRequest(request)
	query := request.URL.Query()
	if !query.Has("n") {
		http.Error(writer, ErrHandshakePartitionCountMissing.Error(), ErrHandshakePartitionCountMissing.Status())
		return
	}
	if n, err := strconv.Atoi(query.Get("n")); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	} else {
		if n != partitionCount {
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
	cursors := parseCursors(partitionCount, query)
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
		WithField("event", h.eventPublisher.GetName()).
		WithField("PartitionCount", partitionCount).
		WithField("partitionID", partitionID).
		WithField("cursors", cursor).
		WithField("PageSizeHint", pageSizeHint).
		WithField("Headers", headers)
	fields.Info()
	serializer := NewNDJSONEventSerializer(writer)
	err := h.eventPublisher.FetchEvents(request.Context(), "", partitionID, cursor, serializer, Options{
		PageSizeHint: pageSizeHint,
	})
	if err != nil {
		logger.WithField("event", h.eventPublisher.GetName()+".fetch_events_error").WithError(err).Info()
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
		return
	}
}
