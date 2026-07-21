package tiledelivery

import (
	"errors"
	"fmt"
	"math"
)

// ScalarAssetLookup returns an already written scalar child tile by its exact
// XYZ identity. Field names and path policy remain with the caller through the
// tier carried by ID.
type ScalarAssetLookup func(ID) (ScalarAsset, bool)

// ScalarParentSampler downsamples one parent tile from the next zoom level.
// Its decoded cache is private to that parent, so it cannot grow with the
// number of tiles in the world or the number of pyramid levels.
type ScalarParentSampler struct {
	parent    ID
	childZoom uint8
	lookup    ScalarAssetLookup
	cache     map[ID][]byte
	peakBytes uint64
}

// NewScalarParentSampler creates the spatial resampling seam for one parent.
// The parent ID chooses the scalar field through its caller-owned tier.
func NewScalarParentSampler(parent ID, lookup ScalarAssetLookup) (*ScalarParentSampler, error) {
	if err := parent.validate(); err != nil {
		return nil, err
	}
	if parent.Zoom >= MaxZoom {
		return nil, errors.New("scalar parent has no valid child zoom")
	}
	if lookup == nil {
		return nil, errors.New("scalar child lookup is required")
	}
	return &ScalarParentSampler{
		parent: parent, childZoom: parent.Zoom + 1, lookup: lookup,
		cache: make(map[ID][]byte),
	}, nil
}

// HasInteriorData reports whether any of the parent's four direct child tiles
// exists. Neighbouring gutter-only data must not cause an empty parent tile to
// be published.
func (sampler *ScalarParentSampler) HasInteriorData() bool {
	if sampler == nil {
		return false
	}
	for childY := int64(sampler.parent.Y) * 2; childY < int64(sampler.parent.Y)*2+2; childY++ {
		for childX := int64(sampler.parent.X) * 2; childX < int64(sampler.parent.X)*2+2; childX++ {
			id, err := NewID(sampler.parent.Tier, sampler.childZoom, childX, childY)
			if err == nil {
				if _, ok := sampler.lookup(id); ok {
					return true
				}
			}
		}
	}
	return false
}

// Sample returns the rounded arithmetic mean of the available four child
// texels. No-data children are excluded from both the total and the divisor;
// a real score of zero remains data. X wraps at the dateline and Y never wraps.
func (sampler *ScalarParentSampler) Sample(sample ScalarSample) (uint8, bool, error) {
	if sampler == nil {
		return 0, false, errors.New("scalar parent sampler is nil")
	}
	childWorld := int64(TileSize) * (int64(1) << sampler.childZoom)
	baseX, baseY := sample.GlobalX*2, sample.GlobalY*2
	total, count := 0, 0
	for offsetY := int64(0); offsetY < 2; offsetY++ {
		childY := baseY + offsetY
		if childY < 0 || childY >= childWorld {
			continue
		}
		for offsetX := int64(0); offsetX < 2; offsetX++ {
			childX := wrapPixel(baseX+offsetX, childWorld)
			value, valid, err := sampler.read(childX, childY)
			if err != nil {
				return 0, false, err
			}
			if valid {
				total += int(value)
				count++
			}
		}
	}
	if count == 0 {
		return 0, false, nil
	}
	return uint8(math.Round(float64(total) / float64(count))), true, nil
}

// PeakDecodedBytes is the largest child-tile cache used by this one parent.
func (sampler *ScalarParentSampler) PeakDecodedBytes() uint64 {
	if sampler == nil {
		return 0
	}
	return sampler.peakBytes
}

func (sampler *ScalarParentSampler) read(globalX, globalY int64) (uint8, bool, error) {
	tileX, tileY := globalX/TileSize, globalY/TileSize
	id, err := NewID(sampler.parent.Tier, sampler.childZoom, tileX, tileY)
	if err != nil {
		return 0, false, err
	}
	if !sampler.childNeededByParent(id) {
		return 0, false, fmt.Errorf("scalar child %d/%d/%d is outside parent %d/%d/%d", id.Zoom, id.X, id.Y, sampler.parent.Zoom, sampler.parent.X, sampler.parent.Y)
	}
	asset, ok := sampler.lookup(id)
	if !ok {
		return 0, false, nil
	}
	if asset.Omitted || asset.ID != id {
		return 0, false, errors.New("scalar child lookup returned an invalid asset")
	}
	encoded, ok := sampler.cache[id]
	if !ok {
		encoded, _, err = ReadScalar(asset.Path, asset.SHA256)
		if err != nil {
			return 0, false, err
		}
		sampler.cache[id] = encoded
		sampler.peakBytes = max(sampler.peakBytes, uint64(len(sampler.cache))*ScalarTileBytes)
	}
	column := int(globalX%TileSize) + ScalarGutter
	row := int(globalY%TileSize) + ScalarGutter
	value := encoded[row*ScalarTextureSize+column]
	if value == ScalarNoData {
		return 0, false, nil
	}
	return value, true, nil
}

func (sampler *ScalarParentSampler) childNeededByParent(id ID) bool {
	if id.Tier != sampler.parent.Tier || id.Zoom != sampler.childZoom {
		return false
	}
	childCount := int64(1) << sampler.childZoom
	parentX, parentY := int64(sampler.parent.X), int64(sampler.parent.Y)
	wantedX := func(candidate int64) bool {
		for offset := int64(-1); offset <= 2; offset++ {
			if wrapPixel(parentX*2+offset, childCount) == candidate {
				return true
			}
		}
		return false
	}
	wantedY := id.Y >= uint32(max(int64(0), parentY*2-1)) && int64(id.Y) <= min(childCount-1, parentY*2+2)
	return wantedX(int64(id.X)) && wantedY
}
