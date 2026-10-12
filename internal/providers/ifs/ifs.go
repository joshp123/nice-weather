// Package ifs retrieves selected surface fields from ECMWF's anonymous IFS
// open-data archive. It stops at complete GRIB messages; decoding and weather
// interpretation belong to the pipeline.
package ifs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// ECMWF publishes the same immutable open-data objects through several
	// official cloud replicas. Google Cloud is the single production transport
	// origin because its range endpoint remains reliable under the forecast
	// build's bounded request pattern.
	DefaultRoot        = "https://storage.googleapis.com/ecmwf-open-data"
	GridDegrees        = 0.25
	MaxIndexBytes      = 32 << 20
	MaxIndexLineBytes  = 2 << 20
	MaxMessageBytes    = 64 << 20
	defaultRunLookback = 6
	minimumBodyTimeout = 30 * time.Second
	minimumBodyRate    = 128 << 10
)

var (
	runHours = [...]int{0, 12}
	// The public 0.25 degree stream publishes 10fg as a one-hour maximum at
	// each three-hourly output time. Those two-hour gaps cannot support an
	// honest daytime gust maximum, so gust is unavailable rather than partial.
	surfaceFields = [...]string{"2t", "2d", "10u", "10v", "tcc", "tp", "ssrd"}
	wantedFields  = func() map[string]struct{} {
		fields := make(map[string]struct{}, len(surfaceFields))
		for _, field := range surfaceFields {
			fields[field] = struct{}{}
		}
		return fields
	}()
)

// RequiredSurfaceFields returns the complete fields required by the weather
// pipeline. Sparse one-hour gust maxima are intentionally excluded.
func RequiredSurfaceFields() []string { return append([]string(nil), surfaceFields[:]...) }

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	Root        string
	HTTP        HTTPDoer
	Now         func() time.Time
	RunLookback int

	// AttemptTimeout is for bounded tests. Production uses a size-derived limit.
	AttemptTimeout time.Duration
}

type Run struct {
	Time time.Time
}

type Message struct {
	parameter string
	offset    int64
	length    int64
	gribURL   string
}

func (m Message) Parameter() string { return m.parameter }
func (m Message) Offset() int64     { return m.offset }
func (m Message) Length() int64     { return m.length }

type NotPublishedError struct{ URL string }

func (e *NotPublishedError) Error() string {
	return fmt.Sprintf("IFS resource is not published: %s", e.URL)
}

type HTTPStatusError struct {
	URL        string
	StatusCode int
	RetryAfter string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("IFS resource %s returned HTTP %d", e.URL, e.StatusCode)
}

func (e *HTTPStatusError) HTTPStatusCode() int     { return e.StatusCode }
func (e *HTTPStatusError) RetryAfterValue() string { return e.RetryAfter }

func NewClient(httpClient HTTPDoer) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		Root:        DefaultRoot,
		HTTP:        httpClient,
		Now:         time.Now,
		RunLookback: defaultRunLookback,
	}
}

// ForecastSteps returns the long-range baseline's published steps. IFS has
// three-hourly output through 144 hours, then six-hourly output through 360.
func ForecastSteps(run Run) ([]int, error) {
	if err := validateRun(run); err != nil {
		return nil, err
	}
	steps := make([]int, 0, 85)
	for step := 0; step <= 144; step += 3 {
		steps = append(steps, step)
	}
	for step := 150; step <= 360; step += 6 {
		steps = append(steps, step)
	}
	return steps, nil
}

func (c *Client) IndexURL(run Run, step int) (string, error) {
	return c.productURL(run, step, ".index")
}

func (c *Client) GRIBURL(run Run, step int) (string, error) {
	return c.productURL(run, step, ".grib2")
}

func (c *Client) productURL(run Run, step int, extension string) (string, error) {
	if err := validateStep(run, step); err != nil {
		return "", err
	}
	root := strings.TrimRight(c.root(), "/")
	parsed, err := url.ParseRequestURI(root)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid IFS root URL %q", root)
	}
	date := run.Time.UTC().Format("20060102")
	hour := run.Time.UTC().Format("15")
	name := fmt.Sprintf("%s%s0000-%dh-oper-fc%s", date, hour, step, extension)
	return fmt.Sprintf("%s/%s/%sz/ifs/0p25/oper/%s", root, date, hour, name), nil
}

func validateRun(run Run) error {
	t := run.Time.UTC()
	if t.IsZero() || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0 {
		return errors.New("IFS run must be an exact UTC hour")
	}
	for _, hour := range runHours {
		if t.Hour() == hour {
			return nil
		}
	}
	return fmt.Errorf("IFS long-range run hour must be 00 or 12 UTC: %02d", t.Hour())
}

func validateStep(run Run, step int) error {
	if err := validateRun(run); err != nil {
		return err
	}
	if step < 0 || step > 360 {
		return fmt.Errorf("IFS forecast step is outside 0..360 hours: %d", step)
	}
	if step <= 144 {
		if step%3 != 0 {
			return fmt.Errorf("IFS forecast step through 144 hours must be 3-hourly: %d", step)
		}
		return nil
	}
	if step < 150 || step%6 != 0 {
		return fmt.Errorf("IFS forecast step after 144 hours must be 6-hourly from 150: %d", step)
	}
	return nil
}

type indexExpectation struct {
	date string
	time string
	step string
}

// ParseIndex validates the selected surface-field rows without constraining
// them to a particular run. FetchIndex additionally binds them to its URL.
func ParseIndex(r io.Reader) (map[string]Message, error) {
	return parseIndex(r, nil)
}

func parseIndex(r io.Reader, expected *indexExpectation) (map[string]Message, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read IFS index: %w", err)
	}
	if len(data) > MaxIndexBytes {
		return nil, fmt.Errorf("IFS index exceeds %d-byte limit", MaxIndexBytes)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), MaxIndexLineBytes)
	messages := make(map[string]Message, len(surfaceFields))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row indexRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse IFS index line %d: %w", lineNumber, err)
		}
		if row.LevelType != "sfc" {
			continue
		}
		if _, wanted := wantedFields[row.Parameter]; !wanted {
			continue
		}
		if expected != nil {
			if row.Domain != "g" || row.Type != "fc" || row.Stream != "oper" || row.Date != expected.date || row.Time != expected.time || row.Step != expected.step {
				return nil, fmt.Errorf("IFS field %q on index line %d has mismatched product metadata", row.Parameter, lineNumber)
			}
		}
		if _, duplicate := messages[row.Parameter]; duplicate {
			return nil, fmt.Errorf("duplicate IFS surface field %q on index line %d", row.Parameter, lineNumber)
		}
		offset, err := row.messageOffset()
		if err != nil {
			return nil, fmt.Errorf("IFS field %q on index line %d: %w", row.Parameter, lineNumber, err)
		}
		length, err := row.messageLength()
		if err != nil {
			return nil, fmt.Errorf("IFS field %q on index line %d: %w", row.Parameter, lineNumber, err)
		}
		if offset < 0 || length <= 0 || length > MaxMessageBytes || offset > math.MaxInt64-(length-1) {
			return nil, fmt.Errorf("IFS field %q on index line %d has unsafe byte range offset=%d length=%d", row.Parameter, lineNumber, offset, length)
		}
		messages[row.Parameter] = Message{parameter: row.Parameter, offset: offset, length: length}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read IFS index: %w", err)
	}
	missing := make([]string, 0)
	for _, field := range surfaceFields {
		if _, ok := messages[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("IFS index is missing required surface fields: %s", strings.Join(missing, ", "))
	}
	return messages, nil
}

type indexRow struct {
	Domain    string          `json:"domain"`
	Date      string          `json:"date"`
	Time      string          `json:"time"`
	Type      string          `json:"type"`
	Stream    string          `json:"stream"`
	Step      string          `json:"step"`
	LevelType string          `json:"levtype"`
	Parameter string          `json:"param"`
	Offset    json.RawMessage `json:"_offset"`
	Length    json.RawMessage `json:"_length"`
	AltOffset json.RawMessage `json:"offset"`
	AltLength json.RawMessage `json:"length"`
}

func (r indexRow) messageOffset() (int64, error) {
	return parseAlternateIndexIntegers(r.Offset, r.AltOffset, "byte offset")
}

func (r indexRow) messageLength() (int64, error) {
	return parseAlternateIndexIntegers(r.Length, r.AltLength, "byte length")
}

func parseAlternateIndexIntegers(primary, alternate json.RawMessage, label string) (int64, error) {
	primarySet := len(primary) > 0 && string(primary) != "null"
	alternateSet := len(alternate) > 0 && string(alternate) != "null"
	if !primarySet && !alternateSet {
		return 0, fmt.Errorf("missing %s", label)
	}
	if !primarySet {
		return parseIndexInteger(alternate)
	}
	primaryValue, err := parseIndexInteger(primary)
	if err != nil {
		return 0, err
	}
	if !alternateSet {
		return primaryValue, nil
	}
	alternateValue, err := parseIndexInteger(alternate)
	if err != nil {
		return 0, err
	}
	if primaryValue != alternateValue {
		return 0, fmt.Errorf("conflicting %s values %d and %d", label, primaryValue, alternateValue)
	}
	return primaryValue, nil
}

func parseIndexInteger(raw json.RawMessage) (int64, error) {
	text := strings.Trim(string(raw), `"`)
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", text)
	}
	return value, nil
}

func (c *Client) FetchIndex(ctx context.Context, run Run, step int) (map[string]Message, error) {
	indexURL, err := c.IndexURL(run, step)
	if err != nil {
		return nil, err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, c.downloadTimeout(MaxIndexBytes))
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build IFS index request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch IFS index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotPublishedError{URL: indexURL}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{URL: indexURL, StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, fmt.Errorf("fetch IFS index: unexpected content encoding %q", encoding)
	}
	expected := &indexExpectation{
		date: run.Time.UTC().Format("20060102"),
		time: run.Time.UTC().Format("1504"),
		step: strconv.Itoa(step),
	}
	messages, err := parseIndex(resp.Body, expected)
	if err != nil {
		return nil, err
	}
	gribURL := strings.TrimSuffix(indexURL, ".index") + ".grib2"
	for parameter, message := range messages {
		message.gribURL = gribURL
		messages[parameter] = message
	}
	return messages, nil
}

// DiscoverLatestCompleteWorldwide finds the newest published 12 UTC run with
// a complete 360-hour index and a GRIB object covering all selected byte
// ranges. The 12 UTC cycle is the only IFS long-range cycle whose horizon can
// cover fourteen complete worldwide local dates. Explicit 00 UTC runs remain
// valid for direct retrieval, but automatic discovery never selects them.
// Only a 404 permits fallback; transport, status, and content errors fail
// closed.
func (c *Client) DiscoverLatestCompleteWorldwide(ctx context.Context) (Run, error) {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	lookback := c.RunLookback
	if lookback <= 0 {
		lookback = defaultRunLookback
	}
	candidate := latestWorldwideCycle(now)
	var failures []string
	for attempt := 0; attempt < lookback; attempt++ {
		run := Run{Time: candidate}
		if err := c.validatePublishedLongRun(ctx, run); err == nil {
			return run, nil
		} else {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Run{}, ctxErr
			}
			var notPublished *NotPublishedError
			if !errors.As(err, &notPublished) {
				return Run{}, fmt.Errorf("validate IFS run %s: %w", candidate.Format(time.RFC3339), err)
			}
			failures = append(failures, fmt.Sprintf("%s: %v", candidate.Format(time.RFC3339), err))
		}
		candidate = candidate.AddDate(0, 0, -1)
	}
	return Run{}, fmt.Errorf("no complete worldwide IFS 12 UTC run found in %d days: %s", lookback, strings.Join(failures, "; "))
}

func (c *Client) validatePublishedLongRun(ctx context.Context, run Run) error {
	messages, err := c.FetchIndex(ctx, run, 360)
	if err != nil {
		return err
	}
	var minimumLength int64
	for _, message := range messages {
		if end := message.offset + message.length; end > minimumLength {
			minimumLength = end
		}
	}
	gribURL, err := c.GRIBURL(run, 360)
	if err != nil {
		return err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, c.downloadTimeout(0))
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodHead, gribURL, nil)
	if err != nil {
		return fmt.Errorf("build IFS GRIB probe: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("probe IFS GRIB: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &NotPublishedError{URL: gribURL}
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{URL: gribURL, StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fmt.Errorf("probe IFS GRIB: unexpected content encoding %q", encoding)
	}
	if resp.ContentLength < minimumLength {
		return fmt.Errorf("probe IFS GRIB: content length %d does not cover selected byte range ending at %d", resp.ContentLength, minimumLength)
	}
	return nil
}

func latestWorldwideCycle(now time.Time) time.Time {
	now = now.UTC()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	if candidate.After(now) {
		candidate = candidate.AddDate(0, 0, -1)
	}
	return candidate
}

func (c *Client) DownloadIndexedMessage(ctx context.Context, run Run, step int, message Message, dst io.Writer) error {
	if _, wanted := wantedFields[message.parameter]; !wanted {
		return fmt.Errorf("unsupported IFS surface field %q", message.parameter)
	}
	if dst == nil {
		return errors.New("IFS destination writer is nil")
	}
	if message.offset < 0 || message.length <= 0 || message.length > MaxMessageBytes || message.offset > math.MaxInt64-(message.length-1) {
		return fmt.Errorf("unsafe IFS byte range offset=%d length=%d", message.offset, message.length)
	}
	gribURL, err := c.GRIBURL(run, step)
	if err != nil {
		return err
	}
	if message.gribURL == "" || message.gribURL != gribURL {
		return errors.New("IFS indexed message does not belong to the requested product")
	}
	return c.downloadRange(ctx, gribURL, message, dst)
}

func (c *Client) downloadRange(ctx context.Context, gribURL string, message Message, dst io.Writer) error {
	end := message.offset + message.length - 1
	attemptCtx, cancel := context.WithTimeout(ctx, c.downloadTimeout(message.length))
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, gribURL, nil)
	if err != nil {
		return fmt.Errorf("build IFS GRIB request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", message.offset, end))
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("download IFS field %s: %w", message.parameter, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return &HTTPStatusError{URL: gribURL, StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fmt.Errorf("download IFS field %s: unexpected content encoding %q", message.parameter, encoding)
	}
	if err := validateContentRange(resp.Header.Get("Content-Range"), message.offset, end); err != nil {
		return fmt.Errorf("download IFS field %s: %w", message.parameter, err)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != message.length {
		return fmt.Errorf("download IFS field %s: content length %d does not match index length %d", message.parameter, resp.ContentLength, message.length)
	}
	var staged bytes.Buffer
	staged.Grow(int(message.length))
	written, err := io.CopyN(&staged, resp.Body, message.length)
	if err != nil {
		return fmt.Errorf("download IFS field %s: wrote %d of %d bytes: %w", message.parameter, written, message.length, err)
	}
	one := make([]byte, 1)
	if n, readErr := resp.Body.Read(one); n != 0 || !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("download IFS field %s: response exceeded indexed length %d", message.parameter, message.length)
	}
	if err := validateGRIB2Envelope(staged.Bytes()); err != nil {
		return fmt.Errorf("download IFS field %s: %w", message.parameter, err)
	}
	if _, err := io.Copy(dst, &staged); err != nil {
		return fmt.Errorf("write IFS field %s: %w", message.parameter, err)
	}
	return nil
}

func validateGRIB2Envelope(message []byte) error {
	if len(message) < 20 {
		return fmt.Errorf("GRIB2 message is too short: %d bytes", len(message))
	}
	if !bytes.Equal(message[:4], []byte("GRIB")) {
		return errors.New("GRIB2 message is missing GRIB prefix")
	}
	if message[7] != 2 {
		return fmt.Errorf("GRIB message has unsupported edition %d", message[7])
	}
	if declared := binary.BigEndian.Uint64(message[8:16]); declared != uint64(len(message)) {
		return fmt.Errorf("GRIB2 declared length %d does not match response length %d", declared, len(message))
	}
	if !bytes.Equal(message[len(message)-4:], []byte("7777")) {
		return errors.New("GRIB2 message is missing 7777 suffix")
	}
	return nil
}

func validateContentRange(header string, start, end int64) error {
	if !strings.HasPrefix(header, "bytes ") {
		return fmt.Errorf("invalid Content-Range %q", header)
	}
	rangePart, total, found := strings.Cut(strings.TrimPrefix(header, "bytes "), "/")
	if !found {
		return fmt.Errorf("invalid Content-Range %q", header)
	}
	startText, endText, found := strings.Cut(rangePart, "-")
	if !found || startText != strconv.FormatInt(start, 10) || endText != strconv.FormatInt(end, 10) {
		return fmt.Errorf("Content-Range %q does not match requested bytes %d-%d", header, start, end)
	}
	if total != "*" {
		totalBytes, err := strconv.ParseInt(total, 10, 64)
		if err != nil || totalBytes <= end {
			return fmt.Errorf("invalid Content-Range %q", header)
		}
	}
	return nil
}

func (c *Client) root() string {
	if c.Root == "" {
		return DefaultRoot
	}
	return c.Root
}

func (c *Client) http() HTTPDoer {
	if c.HTTP == nil {
		return http.DefaultClient
	}
	return c.HTTP
}

func (c *Client) downloadTimeout(expectedBytes int64) time.Duration {
	if c.AttemptTimeout > 0 {
		return c.AttemptTimeout
	}
	if expectedBytes < 0 {
		expectedBytes = 0
	}
	seconds := (expectedBytes + minimumBodyRate - 1) / minimumBodyRate
	return minimumBodyTimeout + time.Duration(seconds)*time.Second
}
