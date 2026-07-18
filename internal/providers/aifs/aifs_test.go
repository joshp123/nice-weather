package aifs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestForecastSteps(t *testing.T) {
	steps := ForecastSteps()
	if len(steps) != 61 || steps[0] != 0 || steps[len(steps)-1] != 360 {
		t.Fatalf("unexpected steps: len=%d first=%d last=%d", len(steps), steps[0], steps[len(steps)-1])
	}
}

func TestProductURLs(t *testing.T) {
	client := NewClient(nil)
	client.Root = "https://example.test/root/"
	run := Run{Time: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
	indexURL, err := client.IndexURL(run, 12)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.test/root/20260718/06z/aifs-single/0p25/oper/20260718060000-12h-oper-fc.index"
	if indexURL != want {
		t.Fatalf("index URL\n got: %s\nwant: %s", indexURL, want)
	}
	gribURL, err := client.GRIBURL(run, 12)
	if err != nil {
		t.Fatal(err)
	}
	if gribURL != strings.TrimSuffix(want, ".index")+".grib2" {
		t.Fatalf("unexpected GRIB URL: %s", gribURL)
	}
}

func TestProductURLRejectsInvalidRunAndStep(t *testing.T) {
	client := NewClient(nil)
	if _, err := client.IndexURL(Run{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}, 0); err == nil {
		t.Fatal("expected invalid run error")
	}
	if _, err := client.IndexURL(Run{Time: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}, 7); err == nil {
		t.Fatal("expected invalid step error")
	}
}

func TestParseIndexSelectsCompleteSurfaceFields(t *testing.T) {
	index := completeIndex(100, 9)
	index = `{"levtype":"pl","param":"2t","_offset":1,"_length":2}` + "\n" + index
	messages, err := ParseIndex(strings.NewReader(index))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != len(surfaceFields) {
		t.Fatalf("got %d messages", len(messages))
	}
	if got := messages["2t"]; got.Offset != 100 || got.Length != 9 {
		t.Fatalf("unexpected 2t message: %+v", got)
	}
}

func TestParseIndexRejectsMalformedDuplicateMissingAndUnsafeRows(t *testing.T) {
	tests := map[string]string{
		"malformed":   completeIndex(0, 2) + "{",
		"duplicate":   completeIndex(0, 2) + `{"levtype":"sfc","param":"2t","_offset":30,"_length":2}` + "\n",
		"missing":     strings.Replace(completeIndex(0, 2), `"param":"ssrd"`, `"param":"other"`, 1),
		"negative":    strings.Replace(completeIndex(0, 2), `"_offset":0`, `"_offset":-1`, 1),
		"zero length": strings.Replace(completeIndex(0, 2), `"_length":2`, `"_length":0`, 1),
		"too large":   strings.Replace(completeIndex(0, 2), `"_length":2`, fmt.Sprintf(`"_length":%d`, MaxMessageBytes+1), 1),
		"not integer": strings.Replace(completeIndex(0, 2), `"_length":2`, `"_length":"no"`, 1),
		"conflicting": strings.Replace(completeIndex(0, 2), `"_length":2`, `"_length":2,"length":3`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseIndex(strings.NewReader(input)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseIndexAcceptsOfficialAndUnprefixedRangeKeys(t *testing.T) {
	input := completeIndex(0, 2)
	input = strings.Replace(input, `"_offset":0,"_length":2`, `"offset":"0","length":"2"`, 1)
	if _, err := ParseIndex(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverLatestChecksNewestFirst(t *testing.T) {
	var requested []string
	httpClient := doerFunc(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.Path)
		if strings.Contains(r.URL.Path, "/06z/") {
			return response(http.StatusNotFound, "", nil), nil
		}
		if r.Method == http.MethodHead {
			resp := response(http.StatusOK, "", nil)
			resp.ContentLength = 14
			return resp, nil
		}
		return response(http.StatusOK, completeIndex(0, 2), nil), nil
	})
	client := NewClient(httpClient)
	client.Root = "https://example.test"
	client.Now = func() time.Time { return time.Date(2026, 7, 18, 9, 30, 0, 0, time.UTC) }
	run, err := client.DiscoverLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	if !run.Time.Equal(want) {
		t.Fatalf("got %s, want %s", run.Time, want)
	}
	if len(requested) != 3 || !strings.Contains(requested[0], "/06z/") || !strings.Contains(requested[1], "/00z/") || !strings.HasSuffix(requested[2], ".grib2") {
		t.Fatalf("not newest-first: %v", requested)
	}
}

func TestDiscoveryFailsClosedOnNon404AndBadContent(t *testing.T) {
	tests := []struct {
		name string
		do   doerFunc
	}{
		{name: "forbidden", do: statusDoer(http.StatusForbidden)},
		{name: "rate limited", do: statusDoer(http.StatusTooManyRequests)},
		{name: "server error", do: statusDoer(http.StatusInternalServerError)},
		{name: "transport", do: func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("network down") }},
		{name: "malformed", do: func(*http.Request) (*http.Response, error) { return response(http.StatusOK, "{", nil), nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := NewClient(doerFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return tt.do(r)
			}))
			client.Root = "https://example.test"
			client.Now = func() time.Time { return time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC) }
			if _, err := client.DiscoverLatest(context.Background()); err == nil {
				t.Fatal("expected error")
			}
			if requests != 1 {
				t.Fatalf("made %d requests; fatal failure must not fall back", requests)
			}
		})
	}
}

func TestDiscoveryRequiresPublishedGRIBCoveringSelectedRanges(t *testing.T) {
	tests := []struct {
		name       string
		gribStatus int
		gribLength int64
	}{
		{name: "GRIB missing", gribStatus: http.StatusNotFound, gribLength: -1},
		{name: "GRIB too short", gribStatus: http.StatusOK, gribLength: 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := NewClient(doerFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.Method == http.MethodGet {
					return response(http.StatusOK, completeIndex(0, 2), nil), nil
				}
				resp := response(tt.gribStatus, "", nil)
				resp.ContentLength = tt.gribLength
				return resp, nil
			}))
			client.Root = "https://example.test"
			client.Now = func() time.Time { return time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC) }
			client.RunLookback = 2
			_, err := client.DiscoverLatest(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.gribStatus == http.StatusOK && requests != 2 {
				t.Fatalf("fatal short GRIB made %d requests", requests)
			}
			if tt.gribStatus == http.StatusNotFound && requests != 4 {
				t.Fatalf("404 GRIB should check older run; made %d requests", requests)
			}
		})
	}
}

func TestDiscoveryPreservesTypedGRIBHeadStatus(t *testing.T) {
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return response(http.StatusOK, completeIndex(0, 2), nil), nil
		}
		return response(http.StatusServiceUnavailable, "", http.Header{"Retry-After": {"11"}}), nil
	}))
	client.Root = "https://example.test"
	client.Now = func() time.Time { return time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC) }
	_, err := client.DiscoverLatest(context.Background())
	var status *HTTPStatusError
	if !errors.As(err, &status) {
		t.Fatalf("error = %v, want typed HTTPStatusError", err)
	}
	if status.StatusCode != http.StatusServiceUnavailable || status.RetryAfter != "11" {
		t.Fatalf("status = %+v", status)
	}
}

func TestProviderCatalogAccessorsReturnCopies(t *testing.T) {
	hours := SupportedRunHours()
	fields := RequiredSurfaceFields()
	hours[0] = 99
	fields[0] = "changed"
	if SupportedRunHours()[0] != 0 || RequiredSurfaceFields()[0] != "2t" {
		t.Fatal("caller mutated provider catalog")
	}
}

func TestParseIndexEnforcesTotalSizeLimit(t *testing.T) {
	valid := completeIndex(0, 2)
	oversized := valid + strings.Repeat(" ", MaxIndexBytes-len(valid)+1)
	if _, err := ParseIndex(strings.NewReader(oversized)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadIndexedMessageUsesExactIdentityRange(t *testing.T) {
	grib := plausibleGRIB2()
	httpClient := doerFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Range"); got != "bytes=4-23" {
			t.Errorf("Range = %q", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding = %q", got)
		}
		header := http.Header{"Content-Range": {"bytes 4-23/40"}, "Content-Length": {"20"}}
		resp := responseBytes(http.StatusPartialContent, grib, header)
		resp.ContentLength = int64(len(grib))
		return resp, nil
	})
	client := NewClient(httpClient)
	client.Root = "https://example.test"
	var dst bytes.Buffer
	err := client.DownloadIndexedMessage(context.Background(), testRun(), 6, Message{Parameter: "2t", Offset: 4, Length: int64(len(grib))}, &dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst.Bytes(), grib) {
		t.Fatalf("downloaded %x", dst.Bytes())
	}
}

func TestDownloadIndexedMessageTimesOutStalledBody(t *testing.T) {
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		header := http.Header{"Content-Range": {"bytes 4-23/40"}, "Content-Length": {"20"}}
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			Header:        header,
			Body:          &contextReadCloser{ctx: request.Context()},
			ContentLength: 20,
		}, nil
	}))
	client.Root = "https://example.test"
	client.AttemptTimeout = 20 * time.Millisecond
	var dst bytes.Buffer
	dst.WriteString("preserve")
	started := time.Now()
	err := client.DownloadIndexedMessage(context.Background(), testRun(), 6, Message{Parameter: "2t", Offset: 4, Length: 20}, &dst)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled body returned after %s", elapsed)
	}
	if dst.String() != "preserve" {
		t.Fatalf("destination changed to %q", dst.String())
	}
}

func TestDownloadIndexedMessageHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		header := http.Header{"Content-Range": {"bytes 4-23/40"}, "Content-Length": {"20"}}
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			Header:        header,
			Body:          &contextReadCloser{ctx: request.Context()},
			ContentLength: 20,
		}, nil
	}))
	client.Root = "https://example.test"
	client.AttemptTimeout = time.Minute
	err := client.DownloadIndexedMessage(ctx, testRun(), 6, Message{Parameter: "2t", Offset: 4, Length: 20}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
}

func TestDownloadIndexedMessageRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		rangeH string
		body   string
		encode string
	}{
		{name: "full response", status: http.StatusOK, rangeH: "bytes 4-7/20", body: "GRIB"},
		{name: "wrong range", status: http.StatusPartialContent, rangeH: "bytes 5-8/20", body: "GRIB"},
		{name: "small total", status: http.StatusPartialContent, rangeH: "bytes 4-7/7", body: "GRIB"},
		{name: "missing range", status: http.StatusPartialContent, body: "GRIB"},
		{name: "short body", status: http.StatusPartialContent, rangeH: "bytes 4-7/20", body: "GR"},
		{name: "long body", status: http.StatusPartialContent, rangeH: "bytes 4-7/20", body: "GRIB!"},
		{name: "encoded", status: http.StatusPartialContent, rangeH: "bytes 4-7/20", body: "GRIB", encode: "gzip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := doerFunc(func(r *http.Request) (*http.Response, error) {
				header := make(http.Header)
				if tt.rangeH != "" {
					header.Set("Content-Range", tt.rangeH)
				}
				if tt.encode != "" {
					header.Set("Content-Encoding", tt.encode)
				}
				return response(tt.status, tt.body, header), nil
			})
			client := NewClient(httpClient)
			client.Root = "https://example.test"
			var dst bytes.Buffer
			dst.WriteString("preserve")
			err := client.DownloadIndexedMessage(context.Background(), testRun(), 6, Message{Parameter: "2t", Offset: 4, Length: 4}, &dst)
			if err == nil {
				t.Fatal("expected error")
			}
			if dst.String() != "preserve" {
				t.Fatalf("destination changed to %q", dst.String())
			}
		})
	}
}

func TestDownloadMessageFetchesValidatedIndex(t *testing.T) {
	grib := plausibleGRIB2()
	httpClient := doerFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".index"):
			return response(http.StatusOK, completeIndex(4, int64(len(grib))), nil), nil
		case strings.HasSuffix(r.URL.Path, ".grib2"):
			return responseBytes(http.StatusPartialContent, grib, http.Header{"Content-Range": {"bytes 4-23/*"}}), nil
		default:
			return response(http.StatusNotFound, "", nil), nil
		}
	})
	client := NewClient(httpClient)
	client.Root = "https://example.test"
	var dst bytes.Buffer
	if err := client.DownloadMessage(context.Background(), testRun(), 6, "2t", &dst); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst.Bytes(), grib) {
		t.Fatalf("got %x", dst.Bytes())
	}
}

func TestDownloadPreservesDestinationForInvalidGRIB2Envelope(t *testing.T) {
	badSuffix := plausibleGRIB2()
	copy(badSuffix[len(badSuffix)-4:], "nope")
	badDeclaredLength := plausibleGRIB2()
	binary.BigEndian.PutUint64(badDeclaredLength[8:16], uint64(len(badDeclaredLength)+1))
	tests := []struct {
		name string
		body []byte
	}{
		{name: "exact-length garbage", body: bytes.Repeat([]byte{'x'}, 20)},
		{name: "truncated envelope", body: badSuffix},
		{name: "wrong declared length", body: badDeclaredLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
				header := http.Header{"Content-Range": {"bytes 4-23/40"}}
				return responseBytes(http.StatusPartialContent, tt.body, header), nil
			}))
			client.Root = "https://example.test"
			var dst bytes.Buffer
			dst.WriteString("preserve")
			err := client.DownloadIndexedMessage(context.Background(), testRun(), 6, Message{Parameter: "2t", Offset: 4, Length: 20}, &dst)
			if err == nil {
				t.Fatal("expected invalid GRIB2 error")
			}
			if dst.String() != "preserve" {
				t.Fatalf("destination changed to %q", dst.String())
			}
		})
	}
}

func completeIndex(start, length int64) string {
	var b strings.Builder
	for i, field := range surfaceFields {
		offset := start + int64(i)*length
		fmt.Fprintf(&b, `{"levtype":"sfc","param":"%s","_offset":%d,"_length":%d}`+"\n", field, offset, length)
	}
	return b.String()
}

func testRun() Run {
	return Run{Time: time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type contextReadCloser struct {
	ctx context.Context
}

func (body *contextReadCloser) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*contextReadCloser) Close() error { return nil }

func response(status int, body string, header http.Header) *http.Response {
	return responseBytes(status, []byte(body), header)
}

func responseBytes(status int, body []byte, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: -1,
	}
}

func plausibleGRIB2() []byte {
	message := make([]byte, 20)
	copy(message[:4], "GRIB")
	message[7] = 2
	binary.BigEndian.PutUint64(message[8:16], uint64(len(message)))
	copy(message[16:], "7777")
	return message
}

func statusDoer(status int) doerFunc {
	return func(*http.Request) (*http.Response, error) {
		return response(status, "", nil), nil
	}
}
