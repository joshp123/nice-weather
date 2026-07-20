package pipeline

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ICON-EU run-average direct radiation has small, physically impossible
// cumulative declines around terrain-shadow and cloud transitions. A live
// Europe audit of every 2..21 h direct/diffuse message in run 2026071800 found
// a 0.064436 MJ/m2 maximum deficit from the per-cell high-water mark. Clamp
// only this negligible solar-energy artifact; a decline above 0.075 remains
// invalid and trips coverage validation.
const solarCumulativeArtifactToleranceMJm2 = .075

func isDWDSolarComponent(field Field) bool {
	return field == SolarDirect || field == SolarDiffuse
}

type valueNormalization struct {
	factor float64
	offset float64
}

func normalizeFrame(frame Frame) (Frame, error) {
	if err := frame.validate(); err != nil {
		return Frame{}, err
	}
	frame, normalization, err := normalizationForFrame(frame)
	if err != nil {
		return Frame{}, err
	}
	for index, value := range frame.Grid.Values {
		frame.Grid.Values[index] = normalizeValue(value, frame, normalization)
	}
	return frame, nil
}

// normalizationForFrame validates the physical unit and interval contract and
// returns canonical frame metadata without touching grid values. Disk-backed
// decoders use the same operation once while preparing a message.
func normalizationForFrame(frame Frame) (Frame, valueNormalization, error) {
	unit := normalizedUnit(frame.Units)
	factor, offset := 1.0, 0.0
	switch frame.Field {
	case Temperature, DewPoint:
		switch unit {
		case "k", "kelvin":
			offset = -273.15
		case "c", "celsius", "degc", "°c":
		default:
			return Frame{}, valueNormalization{}, fmt.Errorf("unsupported temperature unit %q", frame.Units)
		}
		frame.Units = "C"
	case WindU, WindV, Gust:
		switch unit {
		case "m/s", "ms-1", "m s-1", "m s**-1":
			factor = 3.6
		case "km/h", "kmh-1", "km h-1":
		default:
			return Frame{}, valueNormalization{}, fmt.Errorf("unsupported wind unit %q", frame.Units)
		}
		frame.Units = "km/h"
	case CloudCover:
		switch unit {
		case "%", "percent":
		case "0-1", "fraction", "(0-1)", "(0 - 1)", "1":
			factor = 100
		default:
			return Frame{}, valueNormalization{}, fmt.Errorf("unsupported cloud-cover unit %q", frame.Units)
		}
		frame.Units = "%"
	case Rain:
		switch unit {
		case "mm", "kg m-2", "kg m**-2":
		case "m":
			factor = 1000
		default:
			return Frame{}, valueNormalization{}, fmt.Errorf("unsupported precipitation unit %q", frame.Units)
		}
		frame.Units = "mm"
	case Solar, SolarDirect, SolarDiffuse:
		switch unit {
		case "j m-2", "j m**-2", "jm-2":
			factor = 1e-6
		case "mj m-2", "mj m**-2", "mjm-2", "mj/m2":
		case "w m-2", "w m**-2", "wm-2":
			if frame.Semantics != IntervalMean && frame.Semantics != CumulativeMean {
				return Frame{}, valueNormalization{}, fmt.Errorf("solar flux requires average semantics")
			}
			elapsed := frame.End.Sub(frame.Start)
			if elapsed < 0 || (elapsed == 0 && frame.Semantics != CumulativeMean) {
				return Frame{}, valueNormalization{}, fmt.Errorf("solar flux requires a valid average interval")
			}
			factor = elapsed.Seconds() / 1e6
			if frame.Semantics == CumulativeMean {
				frame.Semantics = CumulativeAccumulation
			} else {
				frame.Semantics = IntervalAccumulation
			}
		default:
			return Frame{}, valueNormalization{}, fmt.Errorf("unsupported solar unit %q", frame.Units)
		}
		frame.Units = "MJ/m2"
	default:
		return Frame{}, valueNormalization{}, fmt.Errorf("unsupported pipeline field %q", frame.Field)
	}
	frame.PackingError *= math.Abs(factor)
	frame.PackingQuantum *= math.Abs(factor)
	return frame, valueNormalization{factor: factor, offset: offset}, nil
}

func normalizeValue(value float32, frame Frame, normalization valueNormalization) float32 {
	// Energy accumulated over the zero-length model baseline is exactly zero;
	// the average flux value at that instant is undefined and irrelevant.
	if isSolarField(frame.Field) && frame.Semantics == CumulativeAccumulation && frame.End.Equal(frame.Start) {
		return 0
	}
	if !finite32(value) {
		return value
	}
	normalized := float32(float64(value)*normalization.factor + normalization.offset)
	if isSolarField(frame.Field) && normalized < 0 {
		tolerance := frame.PackingError + float32Roundoff(normalized)
		if isDWDSolarComponent(frame.Field) {
			if tolerance > solarCumulativeArtifactToleranceMJm2 {
				return float32(math.NaN())
			}
			tolerance = solarCumulativeArtifactToleranceMJm2
		}
		if float64(-normalized) <= tolerance {
			return 0
		}
		return float32(math.NaN())
	}
	return normalized
}

func normalizedUnit(unit string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(unit))), " ")
}

// NormalizeSeries turns point samples into explicit duration intervals and
// cumulative IFS fields into interval amounts. The returned frames are the
// only representation accepted by AggregateDay.
func NormalizeSeries(frames []Frame) ([]Frame, error) {
	if len(frames) == 0 {
		return nil, nil
	}
	frames = sortedFrames(frames)
	field, spec := frames[0].Field, frames[0].Grid.Spec
	for index := range frames {
		normalized, err := normalizeFrame(frames[index])
		if err != nil {
			return nil, err
		}
		frames[index] = normalized
		if frames[index].Field != field || !sameSpec(frames[index].Grid.Spec, spec) {
			return nil, fmt.Errorf("forecast series mixes fields or grids")
		}
	}
	result := make([]Frame, 0, len(frames))
	var effectiveCumulative []float32
	var effectivePackingError []float64
	var effectivePackingQuantum []float64
	for index, frame := range frames {
		switch frame.Semantics {
		case Instant:
			if index == 0 {
				continue
			}
			frame.Start = frames[index-1].End
			if !frame.End.After(frame.Start) {
				return nil, fmt.Errorf("instant forecast times are not strictly increasing")
			}
			frame.Semantics = IntervalMean
			result = append(result, frame)
		case CumulativeAccumulation:
			if index == 0 {
				effectiveCumulative = append([]float32(nil), frame.Grid.Values...)
				effectivePackingError = make([]float64, len(frame.Grid.Values))
				effectivePackingQuantum = make([]float64, len(frame.Grid.Values))
				for cell := range effectivePackingError {
					effectivePackingError[cell] = frame.PackingError
					effectivePackingQuantum[cell] = frame.PackingQuantum
				}
				if frame.End.Equal(frame.Start) {
					continue
				}
				// A cumulative product without its zero-hour baseline still has
				// a well-defined first interval from its declared start.
				frame.Semantics = IntervalAccumulation
				result = append(result, frame)
				continue
			}
			previous := frames[index-1]
			if previous.Semantics != CumulativeAccumulation || !previous.Start.Equal(frame.Start) || !frame.End.After(previous.End) {
				return nil, fmt.Errorf("cumulative forecast sequence has a reset or gap at %s", frame.End.Format(time.RFC3339))
			}
			values := make([]float32, len(frame.Grid.Values))
			for cell, current := range frame.Grid.Values {
				prior := effectiveCumulative[cell]
				if !finite32(current) || !finite32(prior) {
					values[cell] = float32(math.NaN())
					effectiveCumulative[cell] = float32(math.NaN())
					continue
				}
				delta := current - prior
				if delta < 0 {
					roundoff := float32Roundoff(delta)
					if field == Rain || field == Solar {
						// These packed run-totals are materialised as Float32 before
						// differencing. Bound that representation error at the two
						// operands; the tiny delta's own ULP does not describe it.
						roundoff = float32RepresentationError(current) + float32RepresentationError(prior)
					}
					tolerance := cumulativePackingTolerance(field, frame.PackingError, effectivePackingError[cell], frame.PackingQuantum, effectivePackingQuantum[cell]) + roundoff
					if isDWDSolarComponent(field) {
						if tolerance > solarCumulativeArtifactToleranceMJm2 {
							values[cell] = float32(math.NaN())
							effectiveCumulative[cell] = float32(math.NaN())
							continue
						}
						tolerance = solarCumulativeArtifactToleranceMJm2
					}
					if float64(-delta) > tolerance {
						values[cell] = float32(math.NaN())
						effectiveCumulative[cell] = float32(math.NaN())
					} else {
						values[cell] = 0
					}
				} else {
					values[cell] = delta
					effectiveCumulative[cell] = current
					effectivePackingError[cell] = frame.PackingError
					effectivePackingQuantum[cell] = frame.PackingQuantum
				}
			}
			frame.Start, frame.Semantics, frame.Grid.Values = previous.End, IntervalAccumulation, values
			result = append(result, frame)
		case IntervalMean, IntervalMaximum, IntervalAccumulation:
			if !frame.End.After(frame.Start) {
				// ICON-EU publishes a zero-step gust maximum at model time.
				// There is no interval over which a maximum could be observed, so
				// discard that one leading optional baseline. Empty intervals later
				// in a series remain a source-contract failure.
				if index == 0 && len(frames) > 1 && field == Gust && frame.Semantics == IntervalMaximum &&
					frame.End.Equal(frame.Start) && frames[1].Semantics == IntervalMaximum &&
					frames[1].Start.Equal(frame.End) && frames[1].End.After(frames[1].Start) {
					continue
				}
				return nil, fmt.Errorf("interval forecast has an empty interval")
			}
			result = append(result, frame)
		default:
			return nil, fmt.Errorf("unknown forecast semantics %d", frame.Semantics)
		}
	}
	return result, nil
}

// The strict bound between two packed samples is the sum of their declared
// maximum absolute errors. Frozen ECMWF open-data audits found that cumulative
// rain and aggregate solar can regress by one exact packing quantum when their
// scale coarsens. The source messages contained no missing values; these are
// encoding artifacts rather than negative rain or radiation. This bound is
// expressed in GRIB packing metadata, so it remains model-independent. DWD's
// direct/diffuse run-average solar uses its separate physical-artifact ceiling.
func cumulativePackingTolerance(field Field, currentError, highWaterError, currentQuantum, highWaterQuantum float64) float64 {
	tolerance := currentError + highWaterError
	if field == Rain || field == Solar {
		// ECMWF IFS rain and solar provide exact scale metadata. The
		// fallback keeps hand-built unit fixtures useful without weakening live
		// decoding, which fails closed when either scale key is absent.
		quantum := math.Max(currentQuantum, highWaterQuantum)
		if quantum == 0 {
			quantum = 2 * math.Max(currentError, highWaterError)
		}
		if field == Solar {
			// Aggregate solar's audited exception is one exact encoded
			// quantum, never a larger inconsistent error declaration.
			return quantum
		}
		tolerance = math.Max(tolerance, quantum)
	}
	return tolerance
}

func float32Roundoff(value float32) float64 {
	if !finite32(value) {
		return 0
	}
	next := math.Nextafter32(value, float32(math.Inf(1)))
	return math.Abs(float64(next - value))
}

// float32RepresentationError bounds the error introduced when one exact
// packed source value is materialised as Float32. Round-to-nearest contributes
// at most half the wider adjacent spacing; infinities never widen the bound.
func float32RepresentationError(value float32) float64 {
	if !finite32(value) {
		return 0
	}
	up := math.Abs(float64(math.Nextafter32(value, float32(math.Inf(1))) - value))
	down := math.Abs(float64(value - math.Nextafter32(value, float32(math.Inf(-1)))))
	if math.IsInf(up, 0) {
		up = 0
	}
	if math.IsInf(down, 0) {
		down = 0
	}
	return .5 * math.Max(up, down)
}
