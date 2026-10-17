package ifs

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

func TestForecastStepsUseOnlyCompleteLongRangeCycles(t *testing.T) {
	steps, err := ForecastSteps(runAt(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 85 || steps[0] != 0 || steps[48] != 144 || steps[49] != 150 || steps[len(steps)-1] != 360 {
		t.Fatalf("steps = %v", steps)
	}
	client := NewClient(nil)
	if _, err := client.IndexURL(runAt(6), 144); err == nil || !strings.Contains(err.Error(), "00 or 12") {
		t.Fatalf("06Z run error = %v", err)
	}
}

func TestRequiredFieldsReturnCopyAndExcludeSparseGust(t *testing.T) {
	fields := RequiredSurfaceFields()
	fields[0] = "changed"
	if got := RequiredSurfaceFields(); len(got) != 6 || got[5] != "ssrd" {
		t.Fatalf("fields = %v", got)
	}
}

func TestProductURLsUsePublicQuarterDegreeIFSControlPath(t *testing.T) {
	client := NewClient(nil)
	client.Root = "https://example.test/root/"
	indexURL, err := client.IndexURL(runAt(12), 360)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.test/root/20260719/12z/ifs/0p25/oper/20260719120000-360h-oper-fc.index"
	if indexURL != want {
		t.Fatalf("URL\n got: %s\nwant: %s", indexURL, want)
	}
	gribURL, err := client.GRIBURL(runAt(12), 360)
	if err != nil {
		t.Fatal(err)
	}
	if gribURL != strings.TrimSuffix(want, ".index")+".grib2" {
		t.Fatalf("GRIB URL = %s", gribURL)
	}
}

func TestDefaultRootUsesTheOfficialGoogleReplicaWithoutChangingProductIdentity(t *testing.T) {
	client := NewClient(nil)
	if client.Root != "https://storage.googleapis.com/ecmwf-open-data" {
		t.Fatalf("default root = %q", client.Root)
	}
	got, err := client.IndexURL(runAt(12), 360)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://storage.googleapis.com/ecmwf-open-data/20260719/12z/ifs/0p25/oper/20260719120000-360h-oper-fc.index"
	if got != want {
		t.Fatalf("default URL\n got: %s\nwant: %s", got, want)
	}
}

func TestParseIndexRequiresEverySelectedField(t *testing.T) {
	messages, err := ParseIndex(strings.NewReader(completeIndex(runAt(0), 360, 100, 20)))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 6 || messages["ssrd"].Length() != 20 {
		t.Fatalf("messages = %v", messages)
	}

	tests := map[string]string{
		"missing solar": strings.Replace(completeIndex(runAt(0), 360, 0, 20), `"param":"ssrd"`, `"param":"other"`, 1),
		"duplicate":     completeIndex(runAt(0), 360, 0, 20) + indexLine(runAt(0), 360, "2t", 999, 20),
		"negative":      strings.Replace(completeIndex(runAt(0), 360, 0, 20), `"_offset":0`, `"_offset":-1`, 1),
		"too large":     strings.Replace(completeIndex(runAt(0), 360, 0, 20), `"_length":20`, fmt.Sprintf(`"_length":%d`, MaxMessageBytes+1), 1),
		"malformed":     completeIndex(runAt(0), 360, 0, 20) + "{",
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
	input := completeIndex(runAt(0), 360, 0, 20)
	input = strings.Replace(input, `"_offset":0,"_length":20`, `"offset":"0","length":"20"`, 1)
	if _, err := ParseIndex(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
}

func TestFetchIndexBindsSelectedRowsToRequestedRunAndStep(t *testing.T) {
	run := runAt(0)
	tests := map[string]string{
		"date":   strings.Replace(completeIndex(run, 360, 0, 20), `"date":"20260719"`, `"date":"20260718"`, 1),
		"time":   strings.Replace(completeIndex(run, 360, 0, 20), `"time":"0000"`, `"time":"1200"`, 1),
		"step":   strings.Replace(completeIndex(run, 360, 0, 20), `"step":"360"`, `"step":"354"`, 1),
		"stream": strings.Replace(completeIndex(run, 360, 0, 20), `"stream":"oper"`, `"stream":"enfo"`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, body, nil), nil
			}))
			client.Root = "https://example.test"
			if _, err := client.FetchIndex(context.Background(), run, 360); err == nil || !strings.Contains(err.Error(), "mismatched product metadata") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseIndexEnforcesTotalSizeLimit(t *testing.T) {
	valid := completeIndex(runAt(0), 360, 0, 20)
	oversized := valid + strings.Repeat(" ", MaxIndexBytes-len(valid)+1)
	if _, err := ParseIndex(strings.NewReader(oversized)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadIndexedMessageUsesExactIdentityRange(t *testing.T) {
	grib := plausibleGRIB2()
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Range"); got != "bytes=4-23" {
			t.Errorf("Range = %q", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding = %q", got)
		}
		header := http.Header{"Content-Range": {"bytes 4-23/40"}, "Content-Length": {"20"}}
		resp := responseBytes(http.StatusPartialContent, grib, header)
		resp.ContentLength = 20
		return resp, nil
	}))
	client.Root = "https://example.test"
	var dst bytes.Buffer
	message := Message{parameter: "2t", offset: 4, length: 20, gribURL: mustGRIBURL(t, client, runAt(0), 360)}
	err := client.DownloadIndexedMessage(context.Background(), runAt(0), 360, message, &dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst.Bytes(), grib) {
		t.Fatalf("body = %x", dst.Bytes())
	}
}

func TestDownloadMessageFetchesAndBindsTheIndex(t *testing.T) {
	grib := plausibleGRIB2()
	run := runAt(0)
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, ".index"):
			return response(http.StatusOK, completeIndex(run, 360, 4, int64(len(grib))), nil), nil
		case strings.HasSuffix(request.URL.Path, ".grib2"):
			return responseBytes(http.StatusPartialContent, grib, http.Header{"Content-Range": {"bytes 4-23/*"}}), nil
		default:
			return response(http.StatusNotFound, "", nil), nil
		}
	}))
	client.Root = "https://example.test"
	var dst bytes.Buffer
	messages, err := client.FetchIndex(context.Background(), run, 360)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadIndexedMessage(context.Background(), run, 360, messages["2t"], &dst); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst.Bytes(), grib) {
		t.Fatalf("body = %x", dst.Bytes())
	}
}

func TestDownloadIndexedMessageRejectsMalformedRangeWithoutTouchingDestination(t *testing.T) {
	client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
		return responseBytes(http.StatusPartialContent, plausibleGRIB2(), http.Header{"Content-Range": {"bytes 5-24/40"}}), nil
	}))
	client.Root = "https://example.test"
	var dst bytes.Buffer
	dst.WriteString("preserve")
	message := Message{parameter: "2t", offset: 4, length: 20, gribURL: mustGRIBURL(t, client, runAt(0), 360)}
	err := client.DownloadIndexedMessage(context.Background(), runAt(0), 360, message, &dst)
	if err == nil {
		t.Fatal("expected error")
	}
	if dst.String() != "preserve" {
		t.Fatalf("destination = %q", dst.String())
	}
}

func TestDownloadIndexedMessageRejectsUnboundOrDifferentProduct(t *testing.T) {
	requests := 0
	client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("request must not be sent")
	}))
	client.Root = "https://example.test"
	message := Message{parameter: "2t", offset: 4, length: 20}

	for name, candidate := range map[string]Message{
		"unbound":         message,
		"different step":  withGRIBURL(message, mustGRIBURL(t, client, runAt(0), 354)),
		"different cycle": withGRIBURL(message, mustGRIBURL(t, client, runAt(12), 360)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := client.DownloadIndexedMessage(context.Background(), runAt(0), 360, candidate, io.Discard); err == nil || !strings.Contains(err.Error(), "does not belong") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("HTTP requests = %d, want 0", requests)
	}
}

func TestDownloadIndexedMessageTimesOutWithoutTouchingDestination(t *testing.T) {
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header:     http.Header{"Content-Range": {"bytes 4-23/40"}},
			Body:       &contextReadCloser{ctx: request.Context()}, ContentLength: 20,
		}, nil
	}))
	client.Root = "https://example.test"
	client.AttemptTimeout = 20 * time.Millisecond
	var dst bytes.Buffer
	dst.WriteString("preserve")
	message := Message{parameter: "2t", offset: 4, length: 20, gribURL: mustGRIBURL(t, client, runAt(0), 360)}
	if err := client.DownloadIndexedMessage(context.Background(), runAt(0), 360, message, &dst); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if dst.String() != "preserve" {
		t.Fatalf("destination = %q", dst.String())
	}
}

func TestDownloadIndexedMessageHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(doerFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Header:     http.Header{"Content-Range": {"bytes 4-23/40"}},
			Body:       &contextReadCloser{ctx: request.Context()}, ContentLength: 20,
		}, nil
	}))
	client.Root = "https://example.test"
	client.AttemptTimeout = time.Minute
	message := Message{parameter: "2t", offset: 4, length: 20, gribURL: mustGRIBURL(t, client, runAt(0), 360)}
	if err := client.DownloadIndexedMessage(ctx, runAt(0), 360, message, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadIndexedMessageRejectsBadResponsesWithoutTouchingDestination(t *testing.T) {
	grib := plausibleGRIB2()
	for _, test := range []struct {
		name   string
		status int
		rangeH string
		body   []byte
		encode string
	}{
		{name: "full response", status: http.StatusOK, rangeH: "bytes 4-23/40", body: grib},
		{name: "wrong range", status: http.StatusPartialContent, rangeH: "bytes 5-24/40", body: grib},
		{name: "small total", status: http.StatusPartialContent, rangeH: "bytes 4-23/23", body: grib},
		{name: "missing range", status: http.StatusPartialContent, body: grib},
		{name: "short body", status: http.StatusPartialContent, rangeH: "bytes 4-23/40", body: grib[:19]},
		{name: "long body", status: http.StatusPartialContent, rangeH: "bytes 4-23/40", body: append(append([]byte(nil), grib...), '!')},
		{name: "encoded", status: http.StatusPartialContent, rangeH: "bytes 4-23/40", body: grib, encode: "gzip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
				header := make(http.Header)
				if test.rangeH != "" {
					header.Set("Content-Range", test.rangeH)
				}
				if test.encode != "" {
					header.Set("Content-Encoding", test.encode)
				}
				return responseBytes(test.status, test.body, header), nil
			}))
			client.Root = "https://example.test"
			var dst bytes.Buffer
			dst.WriteString("preserve")
			message := Message{parameter: "2t", offset: 4, length: 20, gribURL: mustGRIBURL(t, client, runAt(0), 360)}
			if err := client.DownloadIndexedMessage(context.Background(), runAt(0), 360, message, &dst); err == nil {
				t.Fatal("expected error")
			}
			if dst.String() != "preserve" {
				t.Fatalf("destination = %q", dst.String())
			}
		})
	}
}

func TestDownloadPreservesDestinationForInvalidGRIB2Envelope(t *testing.T) {
	badSuffix := plausibleGRIB2()
	copy(badSuffix[len(badSuffix)-4:], "nope")
	badLength := plausibleGRIB2()
	binary.BigEndian.PutUint64(badLength[8:16], uint64(len(badLength)+1))
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "garbage", body: bytes.Repeat([]byte{'x'}, 20)},
		{name: "bad suffix", body: badSuffix},
		{name: "bad declared length", body: badLength},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient(doerFunc(func(*http.Request) (*http.Response, error) {
				return responseBytes(http.StatusPartialContent, test.body, http.Header{"Content-Range": {"bytes 4-23/40"}}), nil
			}))
			client.Root = "https://example.test"
			var dst bytes.Buffer
			dst.WriteString("preserve")
			message := Message{parameter: "2t", offset: 4, length: 20, gribURL: mustGRIBURL(t, client, runAt(0), 360)}
			if err := client.DownloadIndexedMessage(context.Background(), runAt(0), 360, message, &dst); err == nil {
				t.Fatal("expected error")
			}
			if dst.String() != "preserve" {
				t.Fatalf("destination = %q", dst.String())
			}
		})
	}
}

func completeIndex(run Run, step int, start, length int64) string {
	var builder strings.Builder
	for index, field := range surfaceFields {
		builder.WriteString(indexLine(run, step, field, start+int64(index)*length, length))
	}
	return builder.String()
}

func indexLine(run Run, step int, field string, offset, length int64) string {
	return fmt.Sprintf(`{"domain":"g","date":"%s","time":"%s","type":"fc","stream":"oper","step":"%d","levtype":"sfc","param":"%s","_offset":%d,"_length":%d}`+"\n",
		run.Time.UTC().Format("20060102"), run.Time.UTC().Format("1504"), step, field, offset, length)
}

func runAt(hour int) Run {
	return Run{Time: time.Date(2026, 7, 19, hour, 0, 0, 0, time.UTC)}
}

func mustGRIBURL(t *testing.T, client *Client, run Run, step int) string {
	t.Helper()
	value, err := client.GRIBURL(run, step)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func withGRIBURL(message Message, value string) Message {
	message.gribURL = value
	return message
}

func plausibleGRIB2() []byte {
	message := make([]byte, 20)
	copy(message, "GRIB")
	message[7] = 2
	binary.BigEndian.PutUint64(message[8:16], uint64(len(message)))
	copy(message[16:], "7777")
	return message
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func statusDoer(status int) doerFunc {
	return func(*http.Request) (*http.Response, error) { return response(status, "", nil), nil }
}

type contextReadCloser struct{ ctx context.Context }

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
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}
