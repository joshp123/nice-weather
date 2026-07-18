package dwd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type httpClientFunc func(*http.Request) (*http.Response, error)

func (function httpClientFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}

func TestLatestCompleteRunSkipsRunStillBeingPublished(t *testing.T) {
	var requests []string
	client := httpClientFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.String())
		if strings.Contains(request.URL.Path, "2026071812") {
			return response(http.StatusNotFound, ""), nil
		}
		return response(http.StatusOK, ""), nil
	})
	discoverer := Discoverer{Client: client, CandidateCount: 2}
	run, err := discoverer.LatestCompleteRun(context.Background(), time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := run.ID(), "2026071806"; got != want {
		t.Fatalf("latest complete run = %s, want %s", got, want)
	}
	if len(requests) != 1+len(RequiredFields()) {
		t.Fatalf("probe count = %d, want %d", len(requests), 1+len(RequiredFields()))
	}
	for _, request := range requests {
		if !strings.Contains(request, "_120_") {
			t.Fatalf("completeness probe did not use +120 sentinel: %s", request)
		}
		if !strings.HasPrefix(request, "HEAD ") {
			t.Fatalf("completeness probe was not HEAD: %s", request)
		}
	}
}

func TestLatestCompleteRunRejectsUnexpectedProbeStatus(t *testing.T) {
	discoverer := Discoverer{
		Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusTooManyRequests, ""), nil
		}),
		CandidateCount: 1,
	}
	_, err := discoverer.LatestCompleteRun(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v, want unexpected 429 status", err)
	}
	var status *HTTPStatusError
	if !errors.As(err, &status) || status.HTTPStatusCode() != 429 {
		t.Fatalf("error = %T %v, want typed retryable status", err, err)
	}
}

func TestLatestCompleteRunReturnsTransportError(t *testing.T) {
	discoverer := Discoverer{
		Client: httpClientFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		}),
		CandidateCount: 1,
	}
	_, err := discoverer.LatestCompleteRun(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("error = %v, want transport error", err)
	}
}
