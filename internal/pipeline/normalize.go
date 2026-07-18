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

func normalizeFrame(frame Frame) (Frame, error) {
	if err := frame.validate(); err != nil {
		return Frame{}, err
	}
	unit := normalizedUnit(frame.Units)
	factor, offset := 1.0, 0.0
	switch frame.Field {
	case Temperature, DewPoint:
		switch unit {
		case "k", "kelvin":
			offset = -273.15
		case "c", "celsius", "degc", "°c":
		default:
			return Frame{}, fmt.Errorf("unsupported temperature unit %q", frame.Units)
		}
		frame.Units = "C"
	case WindU, WindV, Gust:
		switch unit {
		case "m/s", "ms-1", "m s-1", "m s**-1":
			factor = 3.6
		case "km/h", "kmh-1", "km h-1":
		default:
			return Frame{}, fmt.Errorf("unsupported wind unit %q", frame.Units)
		}
		frame.Units = "km/h"
	case CloudCover:
		switch unit {
		case "%", "percent":
		case "0-1", "fraction", "(0-1)", "(0 - 1)", "1":
			factor = 100
		default:
			return Frame{}, fmt.Errorf("unsupported cloud-cover unit %q", frame.Units)
		}
		frame.Units = "%"
	case Rain:
		switch unit {
		case "mm", "kg m-2", "kg m**-2":
		case "m":
			factor = 1000
		default:
			return Frame{}, fmt.Errorf("unsupported precipitation unit %q", frame.Units)
		}
		frame.Units = "mm"
	case Solar, SolarDirect, SolarDiffuse:
		switch unit {
		case "j m-2", "j m**-2", "jm-2":
			factor = 1e-6
		case "mj m-2", "mj m**-2", "mjm-2", "mj/m2":
		case "w m-2", "w m**-2", "wm-2":
			if frame.Semantics != IntervalMean && frame.Semantics != CumulativeMean {
				return Frame{}, fmt.Errorf("solar flux requires average semantics")
			}
			elapsed := frame.End.Sub(frame.Start)
			if elapsed < 0 || (elapsed == 0 && frame.Semantics != CumulativeMean) {
				return Frame{}, fmt.Errorf("solar flux requires a valid average interval")
			}
			factor = elapsed.Seconds() / 1e6
			if frame.Semantics == CumulativeMean {
				frame.Semantics = CumulativeAccumulation
			} else {
				frame.Semantics = IntervalAccumulation
			}
		default:
			return Frame{}, fmt.Errorf("unsupported solar unit %q", frame.Units)
		}
		frame.Units = "MJ/m2"
	default:
		return Frame{}, fmt.Errorf("unsupported pipeline field %q", frame.Field)
	}
	for index, value := range frame.Grid.Values {
		// Energy accumulated over the zero-length model baseline is exactly zero;
		// the average flux value at that instant is undefined and irrelevant.
		if isSolarField(frame.Field) && frame.Semantics == CumulativeAccumulation && frame.End.Equal(frame.Start) {
			frame.Grid.Values[index] = 0
			continue
		}
		if finite32(value) {
			normalized := float32(float64(value)*factor + offset)
			if isSolarField(frame.Field) && normalized < 0 {
				tolerance := math.Abs(factor)*frame.PackingError + float32Roundoff(normalized)
				if isDWDSolarComponent(frame.Field) {
					if tolerance > solarCumulativeArtifactToleranceMJm2 {
						frame.Grid.Values[index] = float32(math.NaN())
						continue
					}
					tolerance = solarCumulativeArtifactToleranceMJm2
				}
				if float64(-normalized) <= tolerance {
					normalized = 0
				} else {
					normalized = float32(math.NaN())
				}
			}
			frame.Grid.Values[index] = normalized
		}
	}
	frame.PackingError *= math.Abs(factor)
	frame.PackingQuantum *= math.Abs(factor)
	return frame, nil
}

func normalizedUnit(unit string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(unit))), " ")
}

// NormalizeSeries turns point samples into explicit duration intervals and
// cumulative AIFS fields into interval amounts. The returned frames are the
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
					tolerance := cumulativePackingTolerance(field, frame.PackingError, effectivePackingError[cell], frame.PackingQuantum, effectivePackingQuantum[cell]) + float32Roundoff(delta)
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
// maximum absolute errors. Full frozen-run audits found that cumulative rain can
// regress by one exact packing quantum: ICON-EU 2026071800 did so at steps
// 111..117, and AIFS 2026071718 did so after its scale coarsened at step 276.
// These are quantization artifacts, not negative rainfall. Keep the exact-scale
// exception rain-only; solar and every other cumulative field retain the strict
// error bound.
func cumulativePackingTolerance(field Field, currentError, highWaterError, currentQuantum, highWaterQuantum float64) float64 {
	tolerance := currentError + highWaterError
	if field == Rain {
		// Production GRIB rain must provide exact scale metadata. The fallback
		// keeps hand-built non-GRIB unit fixtures useful without weakening live
		// decoding, which fails closed when either scale key is absent.
		quantum := math.Max(currentQuantum, highWaterQuantum)
		if quantum == 0 {
			quantum = 2 * math.Max(currentError, highWaterError)
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
