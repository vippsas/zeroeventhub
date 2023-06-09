package zeroeventhub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io"
	"net/http"
	"strconv"
)

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

// FetchEvents is a client-side implementation that queries the server and properly deserializes received data.
func (c Client) FetchEvents(ctx context.Context, token string, partitionID int, cursor string, r EventReceiver, options Options) error {
	type checkpointOrEvent struct {
		PartitionId int `json:"partition"`
		// either this is set:
		Cursor string `json:"cursor"`
		// OR, these are set:
		Headers map[string]string `json:"headers"`
		Data    json.RawMessage   `json:"data"`
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
	q.Add(fmt.Sprintf("cursor%d", partitionID), cursor)
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
			if err := r.Checkpoint(parsedLine.Cursor); err != nil {
				return err
			}

		} else {
			// event
			if err := r.Event(parsedLine.Data); err != nil {
				return err
			}
		}
	}

	return nil
}
