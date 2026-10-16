package climatefacts

import (
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type Field uint8

const (
	Temperature Field = iota
	DewPoint
	Rain
	DirectRadiation
	EastWind
	NorthWind
)

var FieldNames = [...]string{"t2m", "d2m", "tp", "fdir", "u10", "v10"}

type ChunkKey struct {
	Field       Field
	Time        uint16
	Row, Column uint8
}
type ChunkReference struct {
	Object string
	Offset uint64
	Length uint32
}

type Archive struct {
	Snapshot        string
	Chunks          map[ChunkKey]ChunkReference
	Client          *http.Client
	ReadBytes       atomic.Int64
	manifests       map[Field][]manifestRegion
	nodeIDs         map[Field]string
	loadedManifests map[string]bool
}

// OpenArchive reads a frozen catalogue of public Icechunk object references.
// The catalogue contains metadata only. Weather chunks are read directly from S3.
func OpenArchive(ctx context.Context, path string) (*Archive, error) {
	if path == "" || path == "earthmover" {
		return openPublicArchive(ctx)
	}
	reader, err := open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var input io.Reader = reader
	if strings.HasSuffix(path, ".gz") {
		compressed, e := gzip.NewReader(reader)
		if e != nil {
			return nil, e
		}
		defer compressed.Close()
		input = compressed
	}
	header := make([]byte, 28)
	if _, err = io.ReadFull(input, header); err != nil {
		return nil, err
	}
	if string(header[:8]) != "NWFIDX01" {
		return nil, fmt.Errorf("invalid ERA5 source catalogue header")
	}
	var count uint32
	if err = binary.Read(input, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	archive := &Archive{Snapshot: string(header[8:]), Chunks: make(map[ChunkKey]ChunkReference, count), Client: &http.Client{Timeout: 2 * time.Minute}}
	record := make([]byte, 37)
	for range count {
		if _, err = io.ReadFull(input, record); err != nil {
			return nil, err
		}
		key := ChunkKey{Field: Field(record[0]), Time: binary.LittleEndian.Uint16(record[1:3]), Row: record[3], Column: record[4]}
		archive.Chunks[key] = ChunkReference{Object: string(record[5:25]), Offset: binary.LittleEndian.Uint64(record[25:33]), Length: binary.LittleEndian.Uint32(record[33:37])}
	}
	return archive, nil
}

func open(ctx context.Context, path string) (io.ReadCloser, error) {
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		return os.Open(path)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("read source catalogue: HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func (archive *Archive) ReadChunk(ctx context.Context, key ChunkKey) ([]float32, error) {
	reference, ok := archive.Chunks[key]
	if !ok {
		if err := archive.resolveChunk(ctx, key); err != nil {
			return nil, err
		}
		reference, ok = archive.Chunks[key]
		if !ok {
			return nil, fmt.Errorf("ERA5 catalogue missing %s time=%d row=%d column=%d", FieldNames[key.Field], key.Time, key.Row, key.Column)
		}
	}
	address := "https://earthmover-icechunk-era5.s3.us-east-1.amazonaws.com/icechunkV2/chunks/" + reference.Object
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", reference.Offset, reference.Offset+uint64(reference.Length)-1))
	response, err := archive.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent && !(response.StatusCode == http.StatusOK && reference.Offset == 0) {
		return nil, fmt.Errorf("read ERA5 %s chunk: HTTP %d", FieldNames[key.Field], response.StatusCode)
	}
	encoded, err := io.ReadAll(response.Body)
	archive.ReadBytes.Add(int64(len(encoded)))
	if err != nil {
		return nil, err
	}
	if len(encoded) != int(reference.Length) {
		return nil, fmt.Errorf("ERA5 chunk length %d, expected %d", len(encoded), reference.Length)
	}
	return decodeChunk(encoded)
}
