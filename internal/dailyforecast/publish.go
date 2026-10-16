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
	"sync"
	"sync/atomic"
	"time"
)

type PublicationReport struct {
	RunID       string
	Files       int64
	Bytes       int64
	Elapsed     time.Duration
	AlreadyLive bool
}

// Publish uploads a completed forecast, switches the current pointer last, then prunes old runs.
// The token can be the Site's expiring upload grant or its publisher secret.
func Publish(ctx context.Context, directory, site, token string) (PublicationReport, error) {
	started := time.Now()
	report := PublicationReport{}
	base, err := url.Parse(site)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return report, fmt.Errorf("publish site must be an HTTP or HTTPS URL")
	}
	if strings.TrimSpace(token) == "" {
		return report, fmt.Errorf("publish token is required")
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return report, err
	}
	var manifest Manifest
	if err = json.Unmarshal(encoded, &manifest); err != nil {
		return report, err
	}
	report.RunID = manifest.RunID
	if report.RunID == "" || filepath.Base(directory) != report.RunID {
		return report, fmt.Errorf("forecast directory does not match its manifest run ID")
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	endpoint := func(path string) string { return base.ResolveReference(&url.URL{Path: path}).String() }
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint("/data/forecast/current.json"), nil)
	response, err := client.Do(request)
	if err != nil {
		return report, err
	}
	var current struct {
		RunID string `json:"runId"`
	}
	if response.StatusCode == http.StatusOK {
		err = json.NewDecoder(response.Body).Decode(&current)
	} else if response.StatusCode != http.StatusNotFound {
		err = fmt.Errorf("read current forecast: HTTP %d", response.StatusCode)
	}
	response.Body.Close()
	if err != nil {
		return report, err
	}
	if current.RunID == report.RunID {
		report.AlreadyLive = true
		report.Elapsed = time.Since(started)
		return report, nil
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
	var paths []string
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	uploadContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var next, files, uploadedBytes atomic.Int64
	var workers sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= len(paths) || uploadContext.Err() != nil {
					return
				}
				path := paths[index]
				relative, readErr := filepath.Rel(directory, path)
				var body []byte
				if readErr == nil {
					body, readErr = os.ReadFile(path)
				}
				if readErr == nil {
					readErr = upload(uploadContext, "forecast/"+report.RunID+"/"+filepath.ToSlash(relative), body)
				}
				if readErr != nil {
					errorOnce.Do(func() { firstError = readErr; cancel() })
					return
				}
				files.Add(1)
				uploadedBytes.Add(int64(len(body)))
			}
		}()
	}
	workers.Wait()
	report.Files, report.Bytes = files.Load(), uploadedBytes.Load()
	if firstError != nil {
		return report, firstError
	}
	pointerManifest := struct {
		Manifest
		PreviousRunID *string `json:"previousRunId"`
	}{Manifest: manifest}
	if current.RunID != "" {
		pointerManifest.PreviousRunID = &current.RunID
	}
	pointer, err := json.Marshal(pointerManifest)
	if err != nil {
		return report, err
	}
	if err = upload(ctx, "forecast/current.json", pointer); err != nil {
		return report, err
	}
	report.Files++
	report.Bytes += int64(len(pointer))
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, endpoint("/__prune"), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = client.Do(request)
	if err != nil {
		return report, err
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return report, fmt.Errorf("prune old forecasts: HTTP %d", response.StatusCode)
	}
	report.Elapsed = time.Since(started)
	return report, nil
}
