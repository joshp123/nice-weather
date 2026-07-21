package agera5

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

var ErrChunkNotFound = errors.New("AgERA5 chunk is not present")

type responseError struct {
	message    string
	status     int
	retryAfter string
	cause      error
}

func (err responseError) Error() string           { return err.message }
func (err responseError) Unwrap() error           { return err.cause }
func (err responseError) HTTPStatusCode() int     { return err.status }
func (err responseError) RetryAfterValue() string { return err.retryAfter }

type Client struct {
	root      string
	transport http.RoundTripper
	metricsMu sync.Mutex
	metrics   ClientMetrics
}

// ClientMetrics reports byte counts without retaining request URLs, headers or
// credentials. FetchedBytes includes metadata and coordinate responses;
// CompressedChunkBytes and DecodedChunkBytes cover weather chunks only.
type ClientMetrics struct {
	Responses                   int
	Chunks                      int
	FetchedBytes                int64
	CompressedChunkBytes        int64
	DecodedChunkBytes           int64
	MaximumCompressedChunkBytes int64
	ChunkResponseLimitBytes     int64
}

// NewClient returns a client pinned to the official AgERA5 object store.
// Tests in this package replace the private transport and root directly.
func NewClient() *Client {
	return &Client{
		root: DefaultRoot, transport: http.DefaultTransport,
		metrics: ClientMetrics{ChunkResponseLimitBytes: int64(chunkDecodedBytes + bloscHeaderBytes)},
	}
}

// Metrics returns a race-safe snapshot of the current client's transfer
// counters. It contains no source keys or authentication material.
func (client *Client) Metrics() ClientMetrics {
	if client == nil {
		return ClientMetrics{}
	}
	client.metricsMu.Lock()
	defer client.metricsMu.Unlock()
	return client.metrics
}

// FetchMetadata retrieves and validates the small consolidated metadata
// document. The bearer token is sent only in the Authorization header.
func (client *Client) FetchMetadata(ctx context.Context, token string) (Metadata, error) {
	body, err := client.fetch(ctx, token, ".zmetadata", maxMetadataBytes)
	if err != nil {
		return Metadata{}, err
	}
	metadata, err := parseMetadata(bytes.NewReader(body))
	if err != nil {
		return Metadata{}, err
	}
	timeChunks := make([][]byte, blockCount(metadata.timeLength, timeCoordinateChunk))
	for index := range timeChunks {
		timeChunks[index], err = client.fetch(ctx, token, fmt.Sprintf("time/%d", index), timeCoordinateChunk*4+bloscHeaderBytes)
		if err != nil {
			return Metadata{}, err
		}
	}
	latitude, err := client.fetch(ctx, token, "latitude/0", latitudeCount*8+bloscHeaderBytes)
	if err != nil {
		return Metadata{}, err
	}
	longitude, err := client.fetch(ctx, token, "longitude/0", longitudeCount*8+bloscHeaderBytes)
	if err != nil {
		return Metadata{}, err
	}
	return metadata.bindCoordinates(timeChunks, latitude, longitude)
}

// FetchChunk retrieves, bounds and decodes one source chunk. A missing object
// is reported as ErrChunkNotFound because the official store is sparse over
// fully oceanic blocks.
func (client *Client) FetchChunk(ctx context.Context, token string, reference ChunkRef) (Block, error) {
	if err := validateReference(reference); err != nil {
		return Block{}, err
	}
	body, err := client.fetch(ctx, token, reference.Key(), chunkDecodedBytes+bloscHeaderBytes)
	if err != nil {
		return Block{}, err
	}
	client.recordCompressedChunk(len(body))
	block, err := DecodeChunk(reference, body)
	if err != nil {
		return Block{}, err
	}
	client.recordDecodedChunk(chunkDecodedBytes)
	return block, nil
}

func (client *Client) fetch(ctx context.Context, token, key string, maximumBytes int) ([]byte, error) {
	if err := validateToken(token); err != nil {
		return nil, err
	}
	resourceURL, err := client.resourceURL(key)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create AgERA5 request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.roundTripper().RoundTrip(request)
	if err != nil {
		return nil, fmt.Errorf("fetch AgERA5 %s: %w", key, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		if key == ".zmetadata" || strings.HasPrefix(key, "time/") || key == "latitude/0" || key == "longitude/0" {
			return nil, responseError{message: fmt.Sprintf("required AgERA5 resource %s returned HTTP 404", key), status: response.StatusCode, retryAfter: response.Header.Get("Retry-After")}
		}
		return nil, responseError{message: fmt.Sprintf("%s: %s", ErrChunkNotFound, key), status: response.StatusCode, retryAfter: response.Header.Get("Retry-After"), cause: ErrChunkNotFound}
	}
	if response.StatusCode != http.StatusOK {
		return nil, responseError{message: fmt.Sprintf("fetch AgERA5 %s returned HTTP %d", key, response.StatusCode), status: response.StatusCode, retryAfter: response.Header.Get("Retry-After")}
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, fmt.Errorf("fetch AgERA5 %s returned unsupported content encoding %q", key, encoding)
	}
	if response.ContentLength > int64(maximumBytes) {
		return nil, fmt.Errorf("AgERA5 %s exceeds %d-byte limit", key, maximumBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maximumBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read AgERA5 %s: %w", key, err)
	}
	if len(body) > maximumBytes {
		return nil, fmt.Errorf("AgERA5 %s exceeds %d-byte limit", key, maximumBytes)
	}
	client.recordResponse(len(body))
	return body, nil
}

func (client *Client) recordResponse(bytes int) {
	client.metricsMu.Lock()
	defer client.metricsMu.Unlock()
	client.metrics.Responses++
	client.metrics.FetchedBytes += int64(bytes)
}

func (client *Client) recordCompressedChunk(bytes int) {
	client.metricsMu.Lock()
	defer client.metricsMu.Unlock()
	client.metrics.Chunks++
	client.metrics.CompressedChunkBytes += int64(bytes)
	if int64(bytes) > client.metrics.MaximumCompressedChunkBytes {
		client.metrics.MaximumCompressedChunkBytes = int64(bytes)
	}
}

func (client *Client) recordDecodedChunk(bytes int) {
	client.metricsMu.Lock()
	defer client.metricsMu.Unlock()
	client.metrics.DecodedChunkBytes += int64(bytes)
}

func (client *Client) resourceURL(key string) (string, error) {
	root := strings.TrimRight(client.root, "/")
	parsed, err := url.ParseRequestURI(root)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid AgERA5 root URL %q", root)
	}
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.ContainsAny(key, "?#\\") {
		return "", fmt.Errorf("invalid AgERA5 resource key %q", key)
	}
	return root + "/" + key, nil
}

func (client *Client) roundTripper() http.RoundTripper {
	if client.transport == nil {
		return http.DefaultTransport
	}
	return client.transport
}

func validateToken(token string) error {
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return errors.New("non-empty AgERA5 bearer token without surrounding whitespace is required")
	}
	return nil
}
