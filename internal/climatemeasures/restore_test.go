package climatemeasures

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRestoreDoesNotCheckpointCorruptDownload(t *testing.T) {
	report := Report{WrittenBytes: 5, OutputSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("valid")))}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, ".report.json") {
			json.NewEncoder(response).Encode(report)
			return
		}
		response.Write([]byte("stale"))
	}))
	defer server.Close()
	options := Options{Output: t.TempDir(), TileRow: 17, TileColumn: 118, Year: 2020}
	restored, err := Restore(context.Background(), options, server.URL, "climate-measures")
	if err == nil || restored {
		t.Fatalf("corrupt remote batch marked restored: %v %v", restored, err)
	}
	_, path, checkpoint := batchPaths(options)
	for _, completed := range []string{path, checkpoint} {
		if _, err := os.Stat(completed); !os.IsNotExist(err) {
			t.Fatalf("corrupt download installed %s: %v", completed, err)
		}
	}
}
