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

// NewClient is a constructor for the Client. The partitionCount parameter says how many partitions to expect
// in the V1 protocol; if you do not wish to support the V1 protocol you may pass `NoV1Support`
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

type Partition struct {
	Id                   int   `json:"id,string"`
	Closed               bool  `json:"bool"`
	StartsAfterPartition int   `json:"startsAfterPartition"`
	CursorFromPartitions []int `json:"cursorFromPartitions"`
}

const V1Token = "_v1" // FeedInfo.Token = V1Token indicates to use v1 protocol
const NoV1Support = 0

type FeedInfo struct {
	Token       string      `json:"token"`
	Partitions  []Partition `json:"partitions"`
	ExactlyOnce bool        `json:"exactlyOnce"`
}

func (c Client) Discover(ctx context.Context) (FeedInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return FeedInfo{}, err
	}
	req = req.WithContext(ctx)
	if err = c.requestProcessor(req); err != nil {
		return FeedInfo{}, err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return FeedInfo{}, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(res.Body)

	// Just always read it into a string, because we want to print it if something is wrong..
	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return FeedInfo{}, err
	}

	if res.StatusCode == http.StatusBadRequest && c.partitionCount != NoV1Support {
		// We treat this as a sign that we are on ZeroEventHub v1 because we did not pass the &n parameter;
		// fallback to special version 1 compatability mode. This is only done if
		// partitionCount has been set up when making the client.
		info := FeedInfo{
			Token: V1Token,
		}
		for i := 0; i != c.partitionCount; i++ {
			info.Partitions = append(info.Partitions, Partition{
				Id: i,
			})
		}
		return info, nil
	}

	if res.StatusCode != http.StatusOK {
		if len(responseBody) > 1000 {
			responseBody = responseBody[:1000]
		}
		return FeedInfo{}, errors.Errorf("Unexpected status code: %d. Response body: %s", res.StatusCode, responseBody)
	}

	var info FeedInfo
	err = json.Unmarshal(responseBody, &info)
	if err != nil {
		return FeedInfo{}, errors.Errorf("Failed to unmarshal FeedAPI response, error=%s, response=%s", err, responseBody)
	}

	return info, nil
}

// FetchEvents is a client-side implementation that queries the server and properly deserializes received data.
func (c Client) FetchEvents(ctx context.Context, token string, partitionID int, cursor string, r EventReceiver, options Options) error {
	if token == V1Token {
		return c.FetchEventsV1(ctx, partitionID, cursor, r, options)
	}

	type checkpointOrEvent struct {
		Cursor string `json:"cursor"`
		// OR, this is set:
		Data json.RawMessage `json:"data"`
	}

	req, err := http.NewRequest(http.MethodGet, c.url+"/events", nil)
	if err != nil {
		return err
	}

	req = req.WithContext(ctx)

	q := req.URL.Query()
	q.Add("token", token)
	q.Add("partition", strconv.Itoa(partitionID))
	q.Add("cursor", cursor)
	if options.PageSizeHint != DefaultPageSize {
		q.Add("pagesizehint", fmt.Sprintf("%d", options.PageSizeHint))
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
		_, _ = io.Copy(io.Discard, body)
		_ = body.Close()
	}(res.Body)

	if res.StatusCode/100 != 2 {
		log := c.logger.WithFields(logrus.Fields{
			"responseCode": strconv.Itoa(res.StatusCode),
			"requestUrl":   req.URL.String(),
		}).WithContext(ctx)
		if all, err := io.ReadAll(res.Body); err != nil {
			log.WithField("event", "feedapi.res_body_read_error").WithError(err).Error()
			return err
		} else {
			if string(all) == "\n" || string(all) == "" {
				err = errors.Errorf("response code %d, empty response body", res.StatusCode)
			} else {
				err = errors.Errorf("response code %d, response body: %s", res.StatusCode, string(all))
			}
			log.WithField("event", "feedapi.unexpected_response_body").WithError(err).Error()
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
