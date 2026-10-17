package dailyforecast

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PublicationReport struct {
	RunID   string
	Files   int64
	Bytes   int64
	Elapsed time.Duration
}

// Publish uploads a completed forecast, switches the current pointer last, then prunes old runs.
// The token can be the Site's expiring upload grant or its publisher secret.
func Publish(ctx context.Context, directory, site, token string) (PublicationReport, error) {
	directory = filepath.Clean(directory)
	started := time.Now()
	report := PublicationReport{}
	base, err := url.Parse(site)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return report, fmt.Errorf("publish site must be an HTTP or HTTPS URL")
	}
	if strings.TrimSpace(token) == "" {
		return report, fmt.Errorf("publish token is required")
	}
	encoded, err := os.ReadFile(filepath.Join(filepath.Dir(directory), "current.json"))
	if err != nil {
		return report, err
	}
	var pointer v6Pointer
	if err = json.Unmarshal(encoded, &pointer); err != nil {
		return report, err
	}
	report.RunID = pointer.Run
	if report.RunID == "" || filepath.Base(directory) != report.RunID || pointer.Days != v6Days {
		return report, fmt.Errorf("forecast directory does not match its v6 pointer")
	}
	paths := append([]string{"days.bin"}, v6ScoreImageNames()...)
	bodies := make([][]byte, len(paths))
	for index, name := range paths {
		body, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			return report, readErr
		}
		if len(body) == 0 || (name == "days.bin" && len(body)%(v6Days*15) != 0) {
			return report, fmt.Errorf("invalid v6 forecast file %s", name)
		}
		bodies[index] = body
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	endpoint := func(path string) string { return base.ResolveReference(&url.URL{Path: path}).String() }
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint("/data/v6/forecast/current.json"), nil)
	response, err := client.Do(request)
	if err != nil {
		return report, err
	}
	var previous v6Pointer
	if response.StatusCode == http.StatusOK {
		err = json.NewDecoder(response.Body).Decode(&previous)
	} else if response.StatusCode != http.StatusNotFound {
		err = fmt.Errorf("read v6 forecast: HTTP %d", response.StatusCode)
	}
	response.Body.Close()
	if err != nil {
		return report, err
	}
	pointer.PreviousRun = previous.Run
	if previous.Run == pointer.Run {
		pointer.PreviousRun = previous.PreviousRun
	}

	upload := func(ctx context.Context, key string, body []byte) error {
		for attempt := 1; attempt <= 3; attempt++ {
			request, _ := http.NewRequestWithContext(ctx, http.MethodPut, endpoint("/__publish/"+key), bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+token)
			response, requestErr := client.Do(request)
			status := 0
			if requestErr == nil {
				status = response.StatusCode
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if status == http.StatusCreated {
					return nil
				}
			}
			if attempt == 3 {
				if requestErr != nil {
					return fmt.Errorf("upload %s: %w", key, requestErr)
				}
				return fmt.Errorf("upload %s: HTTP %d", key, status)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		return nil
	}
	for index, name := range paths {
		if err = upload(ctx, "v6/forecast/"+report.RunID+"/"+name, bodies[index]); err != nil {
			return report, err
		}
		report.Files++
		report.Bytes += int64(len(bodies[index]))
	}
	encoded, _ = json.Marshal(pointer)
	if err = upload(ctx, "v6/forecast/current.json", encoded); err != nil {
		return report, err
	}
	report.Files++
	report.Bytes += int64(len(encoded))

	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, endpoint("/__prune"), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	pruneClient := &http.Client{Timeout: 10 * time.Minute}
	response, err = pruneClient.Do(request)
	if err != nil {
		return report, fmt.Errorf("forecast %s published; prune old forecasts: %w", report.RunID, err)
	}
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil {
		return report, fmt.Errorf("forecast %s published; read cleanup result: %w", report.RunID, err)
	}
	if response.StatusCode != http.StatusOK {
		return report, fmt.Errorf("forecast %s published; prune old forecasts: HTTP %d", report.RunID, response.StatusCode)
	}
	report.Elapsed = time.Since(started)
	return report, nil
}

func v6ScoreImageNames() []string {
	names := make([]string, 0, v6Days+1)
	for day := range v6Days {
		names = append(names, fmt.Sprintf("scores-day-%02d.png", day))
	}
	return append(names, "scores-all.png")
}
