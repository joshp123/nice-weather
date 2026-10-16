package climatefacts

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/klauspost/compress/zstd"
)

const SnapshotID = "TZ2KX6A24CJ5DG17T7MG"
const archiveOrigin = "https://earthmover-icechunk-era5.s3.us-east-1.amazonaws.com/icechunkV2/"

var objectEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

type manifestRegion struct {
	Object string
	Bounds [3][2]uint32
}

// Icechunk uses compressed FlatBuffers; this reader needs its snapshot tree
// and native chunk references, rather than its editing or transaction APIs.
type metadataTable struct {
	data     []byte
	position uint32
}

func (t metadataTable) field(slot uint32) uint32 {
	vtable := uint32(int64(t.position) - int64(int32(binary.LittleEndian.Uint32(t.data[t.position:]))))
	entry := uint32(4) + slot*2
	if entry >= uint32(binary.LittleEndian.Uint16(t.data[vtable:])) {
		return 0
	}
	offset := uint32(binary.LittleEndian.Uint16(t.data[vtable+entry:]))
	if offset == 0 {
		return 0
	}
	return t.position + offset
}
func (t metadataTable) table(slot uint32) metadataTable {
	p := t.field(slot)
	return metadataTable{t.data, p + binary.LittleEndian.Uint32(t.data[p:])}
}
func (t metadataTable) vector(slot uint32) (uint32, uint32) {
	p := t.field(slot)
	if p == 0 {
		return 0, 0
	}
	p += binary.LittleEndian.Uint32(t.data[p:])
	return p + 4, binary.LittleEndian.Uint32(t.data[p:])
}
func (t metadataTable) tables(slot uint32) []metadataTable {
	p, n := t.vector(slot)
	out := make([]metadataTable, n)
	for i := range out {
		a := p + uint32(i)*4
		out[i] = metadataTable{t.data, a + binary.LittleEndian.Uint32(t.data[a:])}
	}
	return out
}
func (t metadataTable) string(slot uint32) string {
	p, n := t.vector(slot)
	return string(t.data[p : p+n])
}
func (t metadataTable) number(slot uint32) uint64 {
	p := t.field(slot)
	if p == 0 {
		return 0
	}
	return binary.LittleEndian.Uint64(t.data[p:])
}
func (t metadataTable) object(slot uint32, n uint32) string {
	p := t.field(slot)
	return objectEncoding.EncodeToString(t.data[p : p+n])
}

func (archive *Archive) metadata(ctx context.Context, key string) (metadataTable, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveOrigin+key, nil)
	if err != nil {
		return metadataTable{}, err
	}
	response, err := archive.Client.Do(request)
	if err != nil {
		return metadataTable{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return metadataTable{}, fmt.Errorf("ERA5 %s: HTTP %d", key, response.StatusCode)
	}
	encoded, err := io.ReadAll(response.Body)
	archive.ReadBytes.Add(int64(len(encoded)))
	if err != nil {
		return metadataTable{}, err
	}
	if len(encoded) < 43 || !bytes.Equal(encoded[:12], []byte("ICE🧊CHUNK")) || encoded[36] != 2 || encoded[38] != 1 {
		return metadataTable{}, fmt.Errorf("unsupported Icechunk metadata header: %s", key)
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return metadataTable{}, err
	}
	defer decoder.Close()
	data, err := decoder.DecodeAll(encoded[39:], nil)
	if err != nil {
		return metadataTable{}, err
	}
	if len(data) < 4 {
		return metadataTable{}, fmt.Errorf("empty Icechunk metadata: %s", key)
	}
	return metadataTable{data, binary.LittleEndian.Uint32(data)}, nil
}

func openPublicArchive(ctx context.Context) (archive *Archive, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			archive = nil
			err = fmt.Errorf("invalid ERA5 snapshot metadata: %v", failure)
		}
	}()
	archive = &Archive{Snapshot: SnapshotID, Chunks: make(map[ChunkKey]ChunkReference), Client: &http.Client{Timeout: 2 * time.Minute}, manifests: make(map[Field][]manifestRegion), nodeIDs: make(map[Field]string), loadedManifests: make(map[string]bool)}
	root, err := archive.metadata(ctx, "snapshots/"+SnapshotID)
	if err != nil {
		return nil, err
	}
	for _, node := range root.tables(2) {
		for field, name := range FieldNames {
			if node.string(1) != "/single/temporal/"+name {
				continue
			}
			metadataStart, metadataLength := node.vector(2)
			var schema struct {
				DataType  string `json:"data_type"`
				Shape     []int  `json:"shape"`
				ChunkGrid struct {
					Configuration struct {
						ChunkShape []int `json:"chunk_shape"`
					} `json:"configuration"`
				} `json:"chunk_grid"`
				Codecs []struct {
					Name string `json:"name"`
				} `json:"codecs"`
			}
			if err = json.Unmarshal(node.data[metadataStart:metadataStart+metadataLength], &schema); err != nil {
				return nil, err
			}
			if schema.DataType != "float32" || fmt.Sprint(schema.ChunkGrid.Configuration.ChunkShape) != "[8736 12 12]" || len(schema.Codecs) != 1 || schema.Codecs[0].Name != "numcodecs.pcodec" {
				return nil, fmt.Errorf("ERA5 %s changed its array contract", name)
			}
			archive.nodeIDs[Field(field)] = node.object(0, 8)
			for _, reference := range node.table(4).tables(2) {
				region := manifestRegion{Object: reference.object(0, 12)}
				p, n := reference.vector(1)
				if n != 3 {
					return nil, fmt.Errorf("ERA5 manifest rank is %d", n)
				}
				for dimension := range 3 {
					for bound := range 2 {
						region.Bounds[dimension][bound] = binary.LittleEndian.Uint32(reference.data[p+uint32(dimension*8+bound*4):])
					}
				}
				archive.manifests[Field(field)] = append(archive.manifests[Field(field)], region)
			}
		}
	}
	if len(archive.manifests) != len(FieldNames) {
		return nil, fmt.Errorf("ERA5 snapshot missing required weather fields")
	}
	return archive, nil
}

func (archive *Archive) resolveChunk(ctx context.Context, key ChunkKey) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("invalid ERA5 manifest metadata: %v", failure)
		}
	}()
	coordinate := [3]uint32{uint32(key.Time), uint32(key.Row), uint32(key.Column)}
	for _, region := range archive.manifests[key.Field] {
		inside := true
		for d, v := range coordinate {
			inside = inside && v >= region.Bounds[d][0] && v < region.Bounds[d][1]
		}
		if !inside {
			continue
		}
		if archive.loadedManifests[region.Object] {
			return nil
		}
		root, e := archive.metadata(ctx, "manifests/"+region.Object)
		if e != nil {
			return e
		}
		for _, array := range root.tables(1) {
			if array.object(0, 8) != archive.nodeIDs[key.Field] {
				continue
			}
			for _, reference := range array.tables(1) {
				p, n := reference.vector(0)
				if n != 3 {
					return fmt.Errorf("ERA5 chunk rank is %d", n)
				}
				timeIndex := binary.LittleEndian.Uint32(reference.data[p:])
				row := binary.LittleEndian.Uint32(reference.data[p+4:])
				column := binary.LittleEndian.Uint32(reference.data[p+8:])
				if reference.field(4) == 0 {
					return fmt.Errorf("ERA5 requires a native chunk reference")
				}
				length := reference.number(3)
				if length > 1<<32-1 {
					return fmt.Errorf("ERA5 chunk exceeds 4 GB")
				}
				archive.Chunks[ChunkKey{key.Field, uint16(timeIndex), uint8(row), uint8(column)}] = ChunkReference{reference.object(4, 12), reference.number(2), uint32(length)}
			}
		}
		archive.loadedManifests[region.Object] = true
		return nil
	}
	return fmt.Errorf("no ERA5 manifest for %s %v", FieldNames[key.Field], coordinate)
}
