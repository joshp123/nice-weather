package climatemeasures

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Publish uploads only the requested completed batch below climate-measures/.
// It never changes the app or a live forecast/climate pointer.
func Publish(ctx context.Context, report Report, output, site, token, prefix string) error {
	base, err := url.Parse(site)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return fmt.Errorf("publish site must be an HTTP or HTTPS URL")
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("publish token is required")
	}
	if prefix != "climate-measures" && prefix != "forecast/climate-measures-probe" {
		return fmt.Errorf("publish prefix must be climate-measures or forecast/climate-measures-probe")
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	for _, path := range []string{report.Path, strings.TrimSuffix(report.Path, ".bin.gz") + ".report.json"} {
		relative, err := filepath.Rel(output, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if !strings.HasPrefix(key, "climate-measures/") {
			return fmt.Errorf("batch path outside climate-measures/")
		}
		key = prefix + strings.TrimPrefix(key, "climate-measures")
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, base.ResolveReference(&url.URL{Path: "/__publish/" + key}).String(), file)
		if err != nil {
			file.Close()
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		file.Close()
		if err != nil {
			return err
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			return fmt.Errorf("upload %s: HTTP %d", key, response.StatusCode)
		}
	}
	return nil
}
