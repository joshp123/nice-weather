// Package aifs retrieves selected surface fields from ECMWF's anonymous AIFS
// open-data archive. It deliberately stops at complete GRIB messages; decoding
// and weather-domain interpretation belong to later pipeline stages.
package aifs

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
	DefaultRoot        = "https://ecmwf-forecasts.s3.eu-central-1.amazonaws.com"
	MaxIndexBytes      = 32 << 20
	MaxIndexLineBytes  = 2 << 20
	MaxMessageBytes    = 64 << 20
	defaultRunLookback = 12
	minimumBodyTimeout = 30 * time.Second
	minimumBodyRate    = 128 << 10 // 128 KiB/s, deliberately conservative.
)

var (
	runHours        = [...]int{0, 6, 12, 18}
	surfaceFields   = [...]string{"2t", "2d", "10u", "10v", "tcc", "tp", "ssrd"}
	fieldMembership = func() map[string]struct{} {
		fields := make(map[string]struct{}, len(surfaceFields))
		for _, field := range surfaceFields {
			fields[field] = struct{}{}
		}
		return fields
	}()
)

// SupportedRunHours returns a copy so callers cannot alter provider policy.
func SupportedRunHours() []int {
	return append([]int(nil), runHours[:]...)
}

// RequiredSurfaceFields returns a copy in stable publication order.
func RequiredSurfaceFields() []string {
	return append([]string(nil), surfaceFields[:]...)
}

// HTTPDoer permits deterministic tests and callers with a tuned transport.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	Root        string
	HTTP        HTTPDoer
	Now         func() time.Time
	RunLookback int
	// AttemptTimeout overrides size-derived request deadlines. It is primarily
	// useful for deterministic tests; production callers should leave it zero.
	AttemptTimeout time.Duration
}

type Run struct {
	Time time.Time
}

type Message struct {
	Parameter string
	Offset    int64
	Length    int64
}

// NotPublishedError identifies the one discovery condition that is safe to
// recover from by checking an older run: HTTP 404 from the archive.
type NotPublishedError struct {
	URL string
}

type HTTPStatusError struct {
	URL        string
	StatusCode int
	RetryAfter string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("AIFS resource %s returned HTTP %d", e.URL, e.StatusCode)
}
func (e *HTTPStatusError) HTTPStatusCode() int     { return e.StatusCode }
func (e *HTTPStatusError) RetryAfterValue() string { return e.RetryAfter }

func (e *NotPublishedError) Error() string {
	return fmt.Sprintf("AIFS resource is not published: %s", e.URL)
}

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

func ForecastSteps() []int {
	steps := make([]int, 0, 61)
	for step := 0; step <= 360; step += 6 {
		steps = append(steps, step)
	}
	return steps
}

func (c *Client) IndexURL(run Run, step int) (string, error) {
	return c.productURL(run, step, ".index")
}

func (c *Client) GRIBURL(run Run, step int) (string, error) {
	return c.productURL(run, step, ".grib2")
}

func (c *Client) productURL(run Run, step int, extension string) (string, error) {
	if err := validateRun(run); err != nil {
		return "", err
	}
	if step < 0 || step > 360 || step%6 != 0 {
		return "", fmt.Errorf("AIFS forecast step must be 0..360 in 6-hour increments: %d", step)
	}
	root := strings.TrimRight(c.root(), "/")
	if _, err := url.ParseRequestURI(root); err != nil {
		return "", fmt.Errorf("invalid AIFS root URL: %w", err)
	}
	date := run.Time.UTC().Format("20060102")
	hour := run.Time.UTC().Format("15")
	name := fmt.Sprintf("%s%s0000-%dh-oper-fc%s", date, hour, step, extension)
	return fmt.Sprintf("%s/%s/%sz/aifs-single/0p25/oper/%s", root, date, hour, name), nil
}

func validateRun(run Run) error {
	t := run.Time.UTC()
	if t.IsZero() || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0 {
		return errors.New("AIFS run must be an exact UTC hour")
	}
	for _, hour := range runHours {
		if t.Hour() == hour {
			return nil
		}
	}
	return fmt.Errorf("AIFS run hour must be 00, 06, 12, or 18 UTC: %02d", t.Hour())
}

func (c *Client) root() string {
	if c.Root == "" {
		return DefaultRoot
	}
	return c.Root
}

// ParseIndex returns the seven required surface fields. A successful result is
// complete; missing, duplicate, malformed, or unsafe selected rows are errors.
func ParseIndex(r io.Reader) (map[string]Message, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read AIFS index: %w", err)
	}
	if len(data) > MaxIndexBytes {
		return nil, fmt.Errorf("AIFS index exceeds %d-byte limit", MaxIndexBytes)
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
			return nil, fmt.Errorf("parse AIFS index line %d: %w", lineNumber, err)
		}
		if row.LevelType != "sfc" {
			continue
		}
		if _, wanted := fieldMembership[row.Parameter]; !wanted {
			continue
		}
		if _, duplicate := messages[row.Parameter]; duplicate {
			return nil, fmt.Errorf("duplicate AIFS surface field %q on index line %d", row.Parameter, lineNumber)
		}
		offset, err := row.messageOffset()
		if err != nil {
			return nil, fmt.Errorf("AIFS field %q on index line %d: %w", row.Parameter, lineNumber, err)
		}
		length, err := row.messageLength()
		if err != nil {
			return nil, fmt.Errorf("AIFS field %q on index line %d: %w", row.Parameter, lineNumber, err)
		}
		if offset < 0 || length <= 0 || length > MaxMessageBytes || offset > math.MaxInt64-(length-1) {
			return nil, fmt.Errorf("AIFS field %q on index line %d has unsafe byte range offset=%d length=%d", row.Parameter, lineNumber, offset, length)
		}
		messages[row.Parameter] = Message{Parameter: row.Parameter, Offset: offset, Length: length}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read AIFS index: %w", err)
	}
	missing := make([]string, 0)
	for _, field := range surfaceFields {
		if _, ok := messages[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("AIFS index is missing required surface fields: %s", strings.Join(missing, ", "))
	}
	return messages, nil
}

type indexRow struct {
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

// FetchIndex retrieves and validates one forecast-step index.
func (c *Client) FetchIndex(ctx context.Context, run Run, step int) (map[string]Message, error) {
	indexURL, err := c.IndexURL(run, step)
	if err != nil {
		return nil, err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, c.downloadTimeout(MaxIndexBytes))
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build AIFS index request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch AIFS index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotPublishedError{URL: indexURL}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{URL: indexURL, StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, fmt.Errorf("fetch AIFS index: unexpected content encoding %q", encoding)
	}
	return ParseIndex(resp.Body)
}

// DiscoverLatest finds the newest run at or before Now whose 360-hour index and
// GRIB are both published and internally consistent. Only a 404 permits falling
// back to an older cycle; access, server, transport, and content errors are fatal.
func (c *Client) DiscoverLatest(ctx context.Context) (Run, error) {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	lookback := c.RunLookback
	if lookback <= 0 {
		lookback = defaultRunLookback
	}
	candidate := latestCycle(now)
	var failures []string
	for attempt := 0; attempt < lookback; attempt++ {
		run := Run{Time: candidate}
		if err := c.validatePublishedRun(ctx, run); err == nil {
			return run, nil
		} else {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Run{}, ctxErr
			}
			var notPublished *NotPublishedError
			if !errors.As(err, &notPublished) {
				return Run{}, fmt.Errorf("validate AIFS run %s: %w", candidate.Format(time.RFC3339), err)
			}
			failures = append(failures, fmt.Sprintf("%s: %v", candidate.Format(time.RFC3339), err))
		}
		candidate = candidate.Add(-6 * time.Hour)
	}
	return Run{}, fmt.Errorf("no complete AIFS run found in %d cycles: %s", lookback, strings.Join(failures, "; "))
}

func (c *Client) validatePublishedRun(ctx context.Context, run Run) error {
	messages, err := c.FetchIndex(ctx, run, 360)
	if err != nil {
		return err
	}
	var minimumLength int64
	for _, message := range messages {
		endExclusive := message.Offset + message.Length
		if endExclusive > minimumLength {
			minimumLength = endExclusive
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
		return fmt.Errorf("build AIFS GRIB probe: %w", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("probe AIFS GRIB: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &NotPublishedError{URL: gribURL}
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{URL: gribURL, StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fmt.Errorf("probe AIFS GRIB: unexpected content encoding %q", encoding)
	}
	if resp.ContentLength < minimumLength {
		return fmt.Errorf("probe AIFS GRIB: content length %d does not cover selected byte range ending at %d", resp.ContentLength, minimumLength)
	}
	return nil
}

func latestCycle(now time.Time) time.Time {
	hour := now.Hour() - now.Hour()%6
	return time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
}

// DownloadMessage writes exactly one indexed GRIB message to dst. The index is
// fetched and validated first so callers cannot request an arbitrary range.
func (c *Client) DownloadMessage(ctx context.Context, run Run, step int, parameter string, dst io.Writer) error {
	if dst == nil {
		return errors.New("AIFS destination writer is nil")
	}
	if _, wanted := fieldMembership[parameter]; !wanted {
		return fmt.Errorf("unsupported AIFS surface field %q", parameter)
	}
	messages, err := c.FetchIndex(ctx, run, step)
	if err != nil {
		return err
	}
	message := messages[parameter]
	return c.downloadRange(ctx, run, step, message, dst)
}

// DownloadIndexedMessage writes a previously validated selected message. It is
// useful when a caller fetches an index once and downloads several fields.
func (c *Client) DownloadIndexedMessage(ctx context.Context, run Run, step int, message Message, dst io.Writer) error {
	if _, wanted := fieldMembership[message.Parameter]; !wanted {
		return fmt.Errorf("unsupported AIFS surface field %q", message.Parameter)
	}
	if dst == nil {
		return errors.New("AIFS destination writer is nil")
	}
	if message.Offset < 0 || message.Length <= 0 || message.Length > MaxMessageBytes || message.Offset > math.MaxInt64-(message.Length-1) {
		return fmt.Errorf("unsafe AIFS byte range offset=%d length=%d", message.Offset, message.Length)
	}
	return c.downloadRange(ctx, run, step, message, dst)
}

func (c *Client) downloadRange(ctx context.Context, run Run, step int, message Message, dst io.Writer) error {
	gribURL, err := c.GRIBURL(run, step)
	if err != nil {
		return err
	}
	end := message.Offset + message.Length - 1
	attemptCtx, cancel := context.WithTimeout(ctx, c.downloadTimeout(message.Length))
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, gribURL, nil)
	if err != nil {
		return fmt.Errorf("build AIFS GRIB request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", message.Offset, end))
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("download AIFS field %s: %w", message.Parameter, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return &HTTPStatusError{URL: gribURL, StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fmt.Errorf("download AIFS field %s: unexpected content encoding %q", message.Parameter, encoding)
	}
	if err := validateContentRange(resp.Header.Get("Content-Range"), message.Offset, end); err != nil {
		return fmt.Errorf("download AIFS field %s: %w", message.Parameter, err)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != message.Length {
		return fmt.Errorf("download AIFS field %s: content length %d does not match index length %d", message.Parameter, resp.ContentLength, message.Length)
	}
	// Validate the complete origin response before touching the caller's writer.
	// This makes truncated and oversized range responses non-destructive.
	var staged bytes.Buffer
	staged.Grow(int(message.Length))
	written, err := io.CopyN(&staged, resp.Body, message.Length)
	if err != nil {
		return fmt.Errorf("download AIFS field %s: wrote %d of %d bytes: %w", message.Parameter, written, message.Length, err)
	}
	one := make([]byte, 1)
	if n, readErr := resp.Body.Read(one); n != 0 || !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("download AIFS field %s: response exceeded indexed length %d", message.Parameter, message.Length)
	}
	if err := validateGRIB2Envelope(staged.Bytes()); err != nil {
		return fmt.Errorf("download AIFS field %s: %w", message.Parameter, err)
	}
	if _, err := io.Copy(dst, &staged); err != nil {
		return fmt.Errorf("write AIFS field %s: %w", message.Parameter, err)
	}
	return nil
}

func validateGRIB2Envelope(message []byte) error {
	const minimumGRIB2Bytes = 20 // 16-byte indicator plus 4-byte end section.
	if len(message) < minimumGRIB2Bytes {
		return fmt.Errorf("GRIB2 message is too short: %d bytes", len(message))
	}
	if !bytes.Equal(message[:4], []byte("GRIB")) {
		return errors.New("GRIB2 message is missing GRIB prefix")
	}
	if message[7] != 2 {
		return fmt.Errorf("GRIB message has unsupported edition %d", message[7])
	}
	declaredLength := binary.BigEndian.Uint64(message[8:16])
	if declaredLength != uint64(len(message)) {
		return fmt.Errorf("GRIB2 declared length %d does not match response length %d", declaredLength, len(message))
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
	value := strings.TrimPrefix(header, "bytes ")
	rangePart, total, found := strings.Cut(value, "/")
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
	transferSeconds := (expectedBytes + minimumBodyRate - 1) / minimumBodyRate
	return minimumBodyTimeout + time.Duration(transferSeconds)*time.Second
}
