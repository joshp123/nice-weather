package era5climateproduct

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"nice-weather-finder/internal/delivery"
	"nice-weather-finder/internal/tiledelivery"
)

var (
	mainPointMagic     = [8]byte{'N', 'W', 'E', '5', 'C', 'P', 'T', '1'}
	overridePointMagic = [8]byte{'N', 'W', 'E', '5', 'C', 'D', 'L', '1'}
)

type PointBlock struct {
	LatitudeBlock  int    `json:"latitudeBlock"`
	LongitudeBlock int    `json:"longitudeBlock"`
	LatitudeCount  uint16 `json:"latitudeCount"`
	LongitudeCount uint16 `json:"longitudeCount"`
	Path           string `json:"path"`
	Bytes          uint64 `json:"bytes"`
	SHA256         string `json:"sha256"`
	PayloadSHA256  string `json:"payloadSha256"`
	MediaType      string `json:"mediaType"`
	DataRecords    uint16 `json:"dataRecords"`
	NoDataRecords  uint16 `json:"noDataRecords"`
}

type DatelineBlock struct {
	LatitudeBlock int    `json:"latitudeBlock"`
	LatitudeCount uint16 `json:"latitudeCount"`
	Path          string `json:"path"`
	Bytes         uint64 `json:"bytes"`
	SHA256        string `json:"sha256"`
	PayloadSHA256 string `json:"payloadSha256"`
	MediaType     string `json:"mediaType"`
	DataRecords   uint16 `json:"dataRecords"`
	NoDataRecords uint16 `json:"noDataRecords"`
}

type ScalarTile struct {
	Field        Field  `json:"field"`
	Zoom         uint8  `json:"zoom"`
	X            uint32 `json:"x"`
	Y            uint32 `json:"y"`
	Path         string `json:"path"`
	Bytes        uint64 `json:"bytes"`
	SHA256       string `json:"sha256"`
	MediaType    string `json:"mediaType"`
	DataTexels   uint32 `json:"dataTexels"`
	NoDataTexels uint32 `json:"noDataTexels"`
}

func (builder *Builder) writePointBlocks(releaseRoot string) ([]PointBlock, []DatelineBlock, error) {
	var main []PointBlock
	for latitudeBlock, firstRow := 0, 0; firstRow < Rows; latitudeBlock, firstRow = latitudeBlock+1, firstRow+PointBlockRows {
		latitudeCount := min(PointBlockRows, Rows-firstRow)
		for longitudeBlock, firstColumn := 0, 0; firstColumn < Columns; longitudeBlock, firstColumn = longitudeBlock+1, firstColumn+PointBlockCols {
			longitudeCount := min(PointBlockCols, Columns-firstColumn)
			payload := make([]byte, PointBlockRows*PointBlockCols*delivery.ClimatePointRecordBytes)
			dataRecords := 0
			for row := 0; row < latitudeCount; row++ {
				for column := 0; column < longitudeCount; column++ {
					source := ((firstRow+row)*Columns + firstColumn + column) * delivery.ClimatePointRecordBytes
					target := (row*PointBlockCols + column) * delivery.ClimatePointRecordBytes
					copy(payload[target:target+delivery.ClimatePointRecordBytes], builder.mainRecords[source:source+delivery.ClimatePointRecordBytes])
					if builder.mainRecords[source] != 0 {
						dataRecords++
					}
				}
			}
			if dataRecords == 0 {
				continue
			}
			path := filepath.Join(releaseRoot, "points", builder.period, "main",
				fmt.Sprintf("%03d", latitudeBlock), fmt.Sprintf("%03d.nwc", longitudeBlock))
			asset, err := writePointAsset(path, mainPointMagic, latitudeBlock, longitudeBlock,
				PointBlockRows, PointBlockCols, payload)
			if err != nil {
				return nil, nil, err
			}
			relative, err := relativeReleasePath(releaseRoot, path)
			if err != nil {
				return nil, nil, err
			}
			main = append(main, PointBlock{
				LatitudeBlock: latitudeBlock, LongitudeBlock: longitudeBlock,
				LatitudeCount: uint16(latitudeCount), LongitudeCount: uint16(longitudeCount),
				Path: relative, Bytes: asset.bytes, SHA256: asset.sha256,
				PayloadSHA256: asset.payloadSHA256, MediaType: PointMediaType,
				DataRecords:   uint16(dataRecords),
				NoDataRecords: uint16(PointBlockRows*PointBlockCols - dataRecords),
			})
		}
	}

	var overrides []DatelineBlock
	for latitudeBlock, firstRow := 0, 0; firstRow < Rows; latitudeBlock, firstRow = latitudeBlock+1, firstRow+PointBlockRows {
		latitudeCount := min(PointBlockRows, Rows-firstRow)
		payload := make([]byte, PointBlockRows*delivery.ClimatePointRecordBytes)
		dataRecords := 0
		for row := 0; row < latitudeCount; row++ {
			source := (firstRow + row) * delivery.ClimatePointRecordBytes
			target := row * delivery.ClimatePointRecordBytes
			copy(payload[target:target+delivery.ClimatePointRecordBytes], builder.overrideRecords[source:source+delivery.ClimatePointRecordBytes])
			if builder.overrideRecords[source] != 0 {
				dataRecords++
			}
		}
		if dataRecords == 0 {
			continue
		}
		path := filepath.Join(releaseRoot, "points", builder.period, "dateline",
			fmt.Sprintf("%03d.nwd", latitudeBlock))
		asset, err := writePointAsset(path, overridePointMagic, latitudeBlock, 0,
			PointBlockRows, 1, payload)
		if err != nil {
			return nil, nil, err
		}
		relative, err := relativeReleasePath(releaseRoot, path)
		if err != nil {
			return nil, nil, err
		}
		overrides = append(overrides, DatelineBlock{
			LatitudeBlock: latitudeBlock, LatitudeCount: uint16(latitudeCount),
			Path: relative, Bytes: asset.bytes, SHA256: asset.sha256,
			PayloadSHA256: asset.payloadSHA256, MediaType: OverrideMediaType,
			DataRecords:   uint16(dataRecords),
			NoDataRecords: uint16(PointBlockRows - dataRecords),
		})
	}
	return main, overrides, nil
}

type writtenAsset struct {
	bytes         uint64
	sha256        string
	payloadSHA256 string
}

func writePointAsset(path string, magic [8]byte, latitudeBlock, longitudeBlock, rows, columns int, payload []byte) (writtenAsset, error) {
	if latitudeBlock < 0 || longitudeBlock < 0 || rows < 1 || columns < 1 ||
		rows > PointBlockRows || columns > PointBlockCols ||
		len(payload) != rows*columns*delivery.ClimatePointRecordBytes {
		return writtenAsset{}, errors.New("ERA5 climate point asset geometry is invalid")
	}
	payloadDigest := sha256.Sum256(payload)
	header := make([]byte, pointHeaderBytes)
	copy(header[:8], magic[:])
	putUint16(header[8:10], 1)
	putUint16(header[10:12], pointHeaderBytes)
	putUint16(header[12:14], delivery.ClimatePointRecordBytes)
	putUint16(header[14:16], uint16(rows))
	putUint16(header[16:18], uint16(columns))
	binary.LittleEndian.PutUint32(header[20:24], uint32(latitudeBlock))
	binary.LittleEndian.PutUint32(header[24:28], uint32(longitudeBlock))
	copy(header[32:64], payloadDigest[:])
	body := append(header, payload...)
	digest := sha256.Sum256(body)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return writtenAsset{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return writtenAsset{}, err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return writtenAsset{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return writtenAsset{}, err
	}
	if err := file.Close(); err != nil {
		return writtenAsset{}, err
	}
	return writtenAsset{
		bytes: uint64(len(body)), sha256: hex.EncodeToString(digest[:]),
		payloadSHA256: hex.EncodeToString(payloadDigest[:]),
	}, nil
}

func (builder *Builder) writeScalarTiles(temporaryRoot string) ([]ScalarTile, error) {
	writer, err := tiledelivery.NewWriter(temporaryRoot, builder.config.ReleaseID)
	if err != nil {
		return nil, err
	}
	releaseRoot := filepath.Join(temporaryRoot, "releases", builder.config.ReleaseID)
	var result []ScalarTile
	for fieldIndex, field := range fields {
		written := make(map[tiledelivery.ID]tiledelivery.ScalarAsset)
		maximum := builder.config.MaximumZoom
		if err := builder.writeScalarZoom(writer, releaseRoot, field, maximum,
			builder.scalarSampler(fieldIndex), written, &result); err != nil {
			return nil, err
		}
		for zoom := int(maximum) - 1; zoom >= 0; zoom-- {
			count := 1 << uint8(zoom)
			for y := 0; y < count; y++ {
				for x := 0; x < count; x++ {
					id, err := tiledelivery.NewID(string(field), uint8(zoom), int64(x), int64(y))
					if err != nil {
						return nil, err
					}
					parent, err := tiledelivery.NewScalarParentSampler(id, func(id tiledelivery.ID) (tiledelivery.ScalarAsset, bool) {
						asset, ok := written[id]
						return asset, ok
					})
					if err != nil {
						return nil, err
					}
					if !parent.HasInteriorData() {
						continue
					}
					if err := builder.writeOneScalar(writer, releaseRoot, id, parent.Sample, written, &result); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return result, nil
}

func (builder *Builder) writeScalarZoom(
	writer *tiledelivery.Writer,
	releaseRoot string,
	field Field,
	zoom uint8,
	sampler tiledelivery.EvaluatedScoreSampler,
	written map[tiledelivery.ID]tiledelivery.ScalarAsset,
	result *[]ScalarTile,
) error {
	count := 1 << zoom
	for y := 0; y < count; y++ {
		for x := 0; x < count; x++ {
			id, err := tiledelivery.NewID(string(field), zoom, int64(x), int64(y))
			if err != nil {
				return err
			}
			if err := builder.writeOneScalar(writer, releaseRoot, id, sampler, written, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func (builder *Builder) writeOneScalar(
	writer *tiledelivery.Writer,
	releaseRoot string,
	id tiledelivery.ID,
	sampler tiledelivery.EvaluatedScoreSampler,
	written map[tiledelivery.ID]tiledelivery.ScalarAsset,
	result *[]ScalarTile,
) error {
	asset, err := writer.WriteScalar(id, builder.period, sampler)
	if err != nil {
		return fmt.Errorf("write ERA5 climate scalar %s %d/%d/%d: %w", id.Tier, id.Zoom, id.X, id.Y, err)
	}
	if asset.Omitted {
		return nil
	}
	relative, err := relativeReleasePath(releaseRoot, asset.Path)
	if err != nil {
		return err
	}
	written[id] = asset
	*result = append(*result, ScalarTile{
		Field: Field(id.Tier), Zoom: id.Zoom, X: id.X, Y: id.Y,
		Path: relative, Bytes: asset.Bytes, SHA256: asset.SHA256,
		MediaType: asset.MediaType, DataTexels: asset.DataTexels,
		NoDataTexels: asset.NoDataTexels,
	})
	return nil
}

func (builder *Builder) scalarSampler(field int) tiledelivery.EvaluatedScoreSampler {
	return func(sample tiledelivery.ScalarSample) (uint8, bool, error) {
		row := int(math.Round((90 - sample.Latitude) / GridDegrees))
		if row < 0 || row >= Rows {
			return 0, false, nil
		}
		// The interval east of the midpoint belongs to the independent +180°
		// civil-day cell, when the source plan contains that cell.
		if sample.Longitude >= 180-GridDegrees/2 {
			if builder.displayMask.values[row*Columns] == 0 ||
				!builder.overridePresent[row] || builder.overrideRecords[row*delivery.ClimatePointRecordBytes] == 0 {
				return 0, false, nil
			}
			return builder.overrideScalars[field][row], true, nil
		}
		column := int(math.Round((sample.Longitude + 180) / GridDegrees))
		if column < 0 || column >= Columns {
			return 0, false, nil
		}
		offset := row*Columns + column
		if builder.displayMask.values[offset] == 0 || builder.mainRecords[offset*delivery.ClimatePointRecordBytes] == 0 {
			return 0, false, nil
		}
		return builder.mainScalars[field][offset], true, nil
	}
}
