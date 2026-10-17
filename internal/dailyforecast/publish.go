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
		RunID         string  `json:"runId"`
		PreviousRunID *string `json:"previousRunId"`
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

	v6Directory := filepath.Join(filepath.Dir(filepath.Dir(directory)), "v6", "forecast", report.RunID)
	v6DaysPath := filepath.Join(v6Directory, "days.bin")
	if body, readErr := os.ReadFile(v6DaysPath); readErr == nil {
		var pointer v6Pointer
		localPointer, readErr := os.ReadFile(filepath.Join(filepath.Dir(v6Directory), "current.json"))
		if readErr != nil {
			return report, readErr
		}
		if readErr = json.Unmarshal(localPointer, &pointer); readErr != nil {
			return report, readErr
		}
		if pointer.Run != report.RunID || pointer.Days != v6Days || len(body) == 0 || len(body)%(v6Days*15) != 0 {
			return report, fmt.Errorf("invalid v6 forecast publication")
		}
		var scoreImages []struct {
			name string
			body []byte
		}
		for _, name := range v6ScoreImageNames() {
			imageBody, imageErr := os.ReadFile(filepath.Join(v6Directory, name))
			if imageErr != nil {
				return report, imageErr
			}
			if len(imageBody) == 0 {
				return report, fmt.Errorf("empty v6 score image %s", name)
			}
			scoreImages = append(scoreImages, struct {
				name string
				body []byte
			}{name, imageBody})
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint("/data/v6/forecast/current.json"), nil)
		response, readErr := client.Do(request)
		if readErr != nil {
			return report, readErr
		}
		var previous v6Pointer
		if response.StatusCode == http.StatusOK {
			readErr = json.NewDecoder(response.Body).Decode(&previous)
		} else if response.StatusCode != http.StatusNotFound {
			readErr = fmt.Errorf("read v6 forecast: HTTP %d", response.StatusCode)
		}
		response.Body.Close()
		if readErr != nil {
			return report, readErr
		}
		pointer.PreviousRun = previous.Run
		if previous.Run == pointer.Run {
			pointer.PreviousRun = previous.PreviousRun
		}
		if err = upload(ctx, "v6/forecast/"+report.RunID+"/days.bin", body); err != nil {
			return report, err
		}
		report.Files++
		report.Bytes += int64(len(body))
		for _, scoreImage := range scoreImages {
			if err = upload(ctx, "v6/forecast/"+report.RunID+"/"+scoreImage.name, scoreImage.body); err != nil {
				return report, err
			}
			report.Files++
			report.Bytes += int64(len(scoreImage.body))
		}
		encoded, _ := json.Marshal(pointer)
		if err = upload(ctx, "v6/forecast/current.json", encoded); err != nil {
			return report, err
		}
		report.Files++
		report.Bytes += int64(len(encoded))
	} else if !os.IsNotExist(readErr) {
		return report, readErr
	}
	pointerManifest := struct {
		Manifest
		PreviousRunID *string `json:"previousRunId"`
	}{Manifest: manifest}
	if current.RunID == report.RunID {
		pointerManifest.PreviousRunID = current.PreviousRunID
	} else if current.RunID != "" {
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
