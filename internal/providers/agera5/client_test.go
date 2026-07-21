package agera5

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetchMetadataUsesBearerAuthAndValidatesResponse(t *testing.T) {
	token := "secret-test-token"
	coordinates := coordinateFixtures(t, 17362)
	client := NewClient()
	client.transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding = %q", got)
		}
		key := strings.TrimPrefix(request.URL.Path, "/store/")
		if key == ".zmetadata" {
			return response(http.StatusOK, metadataFixture(t), nil), nil
		}
		body, ok := coordinates[key]
		if !ok {
			t.Fatalf("unexpected coordinate request %s", request.URL)
		}
		return response(http.StatusOK, body, nil), nil
	})
	client.root = "https://example.test/store/"
	metadata, err := client.FetchMetadata(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.TimeLength() != 17362 {
		t.Fatalf("time length = %d", metadata.TimeLength())
	}
}

func TestFetchChunkBoundsBodyAndDoesNotLeakToken(t *testing.T) {
	token := "do-not-print-this"
	reference := oneReference(t)
	values := make([]float32, chunkValueCount)
	for index := range values {
		values[index] = 285
	}
	body := bloscFixture(t, floatBytesForTest(values), chunkDecodedBytes, false)
	client := NewClient()
	client.transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/"+reference.Key()) {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing bearer token")
		}
		return response(http.StatusOK, body, nil), nil
	})
	client.root = "https://example.test/store"
	block, err := client.FetchChunk(context.Background(), token, reference)
	if err != nil {
		t.Fatal(err)
	}
	value, valid, err := block.Value(0, 0, 0)
	if err != nil || !valid || value != 285 {
		t.Fatalf("value=%g valid=%t error=%v", value, valid, err)
	}
	metrics := client.Metrics()
	if metrics.Responses != 1 || metrics.Chunks != 1 || metrics.FetchedBytes != int64(len(body)) || metrics.CompressedChunkBytes != int64(len(body)) || metrics.DecodedChunkBytes != chunkDecodedBytes || metrics.MaximumCompressedChunkBytes != int64(len(body)) || metrics.ChunkResponseLimitBytes != chunkDecodedBytes+bloscHeaderBytes {
		t.Fatalf("client metrics = %+v", metrics)
	}

	client.transport = roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") })
	_, err = client.FetchChunk(context.Background(), token, reference)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("transport error = %v", err)
	}
	if got := client.Metrics(); got != metrics {
		t.Fatalf("failed request changed metrics: got %+v want %+v", got, metrics)
	}
}

func TestFetchChunkHandlesSparseAndHostileResponses(t *testing.T) {
	reference := oneReference(t)
	tests := []struct {
		name string
		do   roundTripperFunc
		want error
	}{
		{"missing", func(*http.Request) (*http.Response, error) { return response(http.StatusNotFound, nil, nil), nil }, ErrChunkNotFound},
		{"forbidden", func(*http.Request) (*http.Response, error) { return response(http.StatusForbidden, nil, nil), nil }, nil},
		{"encoded", func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, []byte("bad"), http.Header{"Content-Encoding": {"gzip"}}), nil
		}, nil},
		{"oversize declared", func(*http.Request) (*http.Response, error) {
			result := response(http.StatusOK, nil, nil)
			result.ContentLength = chunkDecodedBytes + bloscHeaderBytes + 1
			return result, nil
		}, nil},
		{"oversize streamed", func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, make([]byte, chunkDecodedBytes+bloscHeaderBytes+1), nil), nil
		}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient()
			client.transport = test.do
			client.root = "https://example.test/store"
			_, err := client.FetchChunk(context.Background(), "token", reference)
			if err == nil {
				t.Fatal("expected error")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestClientRejectsUnsafeRootKeyAndTokenBeforeRequest(t *testing.T) {
	calls := 0
	client := NewClient()
	client.transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected")
	})
	client.root = "http://example.test"
	if _, err := client.FetchMetadata(context.Background(), "token"); err == nil {
		t.Fatal("expected root error")
	}
	client.root = "https://example.test"
	if _, err := client.FetchMetadata(context.Background(), " token\n"); err == nil {
		t.Fatal("expected token error")
	}
	if calls != 0 {
		t.Fatalf("requests = %d", calls)
	}
}

func TestClientDoesNotFollowBearerRedirects(t *testing.T) {
	calls := 0
	client := NewClient()
	client.transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls > 1 {
			t.Fatalf("redirect target received Authorization %q", request.Header.Get("Authorization"))
		}
		return response(http.StatusFound, nil, http.Header{"Location": {"http://attacker.test/token"}}), nil
	})
	client.root = "https://example.test/store"
	if _, err := client.FetchMetadata(context.Background(), "secret-token"); err == nil {
		t.Fatal("expected redirect status error")
	}
	if calls != 1 {
		t.Fatalf("requests = %d", calls)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body []byte, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}
