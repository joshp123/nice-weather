package climatefacts

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"
)

// The optional live check exercises the pinned public metadata and decoder,
// then compares every decoded float with the independent Icechunk/Pcodec reader.
func TestPublicArchive(t *testing.T) {
	if os.Getenv("NWF_CLIMATE_ARCHIVE_CHECK") == "" {
		t.Skip("set NWF_CLIMATE_ARCHIVE_CHECK for public archive I/O")
	}
	archive, err := OpenArchive(context.Background(), "earthmover")
	if err != nil {
		t.Fatal(err)
	}
	key := ChunkKey{Temperature, 84, 17, 118}
	values, err := archive.ReadChunk(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 8736*144 {
		t.Fatalf("decoded %d values", len(values))
	}
	raw := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(value))
	}
	if digest := fmt.Sprintf("%x", sha256.Sum256(raw)); digest != "33dce0fc238ef0aca0a87b8ea7b0f87b00f9915d8e3a076c75d12d4153388bad" {
		t.Fatalf("decoded public tile differs from independent reader: %s", digest)
	}
	t.Logf("snapshot %s; %d decoded values; %d source bytes; first value %.8f K", archive.Snapshot, len(values), archive.ReadBytes.Load(), values[0])
}
