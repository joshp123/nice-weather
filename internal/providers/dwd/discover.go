package dwd

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Discoverer struct {
	Client          HTTPClient
	BaseURL         string
	CandidateCount  int
	CompletenessSet []Field
}

func (discoverer Discoverer) LatestCompleteRun(ctx context.Context, now time.Time) (Run, error) {
	if discoverer.Client == nil {
		return Run{}, fmt.Errorf("DWD run discovery requires an HTTP client")
	}
	baseURL := discoverer.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	candidateCount := discoverer.CandidateCount
	if candidateCount == 0 {
		candidateCount = 12
	}
	fields := discoverer.CompletenessSet
	if len(fields) == 0 {
		fields = RequiredFields()
	}
	runs, err := CandidateRuns(now, candidateCount)
	if err != nil {
		return Run{}, err
	}
	for _, run := range runs {
		complete, err := discoverer.complete(ctx, baseURL, run, fields)
		if err != nil {
			return Run{}, err
		}
		if complete {
			return run, nil
		}
	}
	return Run{}, fmt.Errorf("no complete DWD ICON-EU run found in the latest %d cycles", candidateCount)
}

func (discoverer Discoverer) complete(ctx context.Context, baseURL string, run Run, fields []Field) (bool, error) {
	for _, field := range fields {
		file, err := FileFor(baseURL, run, field, MaxForecastHour)
		if err != nil {
			return false, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, file.URL, nil)
		if err != nil {
			return false, err
		}
		response, err := discoverer.Client.Do(request)
		if err != nil {
			return false, fmt.Errorf("probe %s: %w", file.URL, err)
		}
		response.Body.Close()
		switch response.StatusCode {
		case http.StatusOK:
			continue
		case http.StatusNotFound:
			return false, nil
		default:
			return false, &HTTPStatusError{URL: file.URL, StatusCode: response.StatusCode, RetryAfter: response.Header.Get("Retry-After")}
		}
	}
	return true, nil
}
