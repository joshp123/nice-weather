package climatemeasures

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Restore downloads a completed remote batch into a fresh local output root.
// Build subsequently checks the restored batch against the requested inputs.
func Restore(ctx context.Context, options Options, site, prefix string) (bool, error) {
	directory, path, checkpoint := batchPaths(options)
	if _, err := os.Stat(checkpoint); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	base, err := url.Parse(site)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return false, fmt.Errorf("restore site must be an HTTP or HTTPS URL")
	}
	if prefix != "climate-measures" && prefix != "forecast/climate-measures-probe" {
		return false, fmt.Errorf("restore prefix must be climate-measures or forecast/climate-measures-probe")
	}
	key := fmt.Sprintf("%s/r%02d-c%03d/%d", prefix, options.TileRow, options.TileColumn, options.Year)
	client := &http.Client{Timeout: 2 * time.Minute}
	get := func(suffix string) (*http.Response, error) {
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, base.ResolveReference(&url.URL{Path: "/data/" + key + suffix}).String(), nil)
		if e != nil {
			return nil, e
		}
		return client.Do(request)
	}
	response, err := get(".report.json")
	if err != nil {
		return false, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return false, fmt.Errorf("read remote batch report: HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return false, err
	}
	var report Report
	if err = json.Unmarshal(encoded, &report); err != nil {
		return false, err
	}
	response, err = get(".bin.gz")
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("read completed remote batch: HTTP %d", response.StatusCode)
	}
	if err = os.MkdirAll(directory, 0755); err != nil {
		return false, err
	}
	file, err := os.Create(path + ".partial")
	if err != nil {
		return false, err
	}
	length, copyErr := io.Copy(file, response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if length != report.WrittenBytes {
		return false, fmt.Errorf("remote batch length %d, checkpoint expects %d", length, report.WrittenBytes)
	}
	sum, err := fileSHA256(path + ".partial")
	if err != nil {
		return false, err
	}
	if sum != report.OutputSHA256 {
		return false, fmt.Errorf("remote batch checksum does not match its checkpoint")
	}
	if err = os.Rename(path+".partial", path); err != nil {
		return false, err
	}
	// Store only the expected local path; a cloud runner's path has no authority here.
	report.Path = path
	encoded, err = json.MarshalIndent(report, "", "  ")
	if err != nil {
		return false, err
	}
	if err = os.WriteFile(checkpoint+".partial", encoded, 0644); err != nil {
		return false, err
	}
	if err = os.Rename(checkpoint+".partial", checkpoint); err != nil {
		return false, err
	}
	return true, nil
}

func batchPaths(options Options) (directory, path, checkpoint string) {
	directory = filepath.Join(options.Output, "climate-measures", fmt.Sprintf("r%02d-c%03d", options.TileRow, options.TileColumn))
	return directory, filepath.Join(directory, fmt.Sprintf("%d.bin.gz", options.Year)), filepath.Join(directory, fmt.Sprintf("%d.report.json", options.Year))
}
