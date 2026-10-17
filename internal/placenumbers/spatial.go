// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
package placenumbers

import "math"

type PointWeight struct {
	Index  int     `json:"index"`
	Weight float64 `json:"weight"`
}

type WeightedNumbers struct {
	Numbers Numbers `json:"numbers"`
	Weight  float64 `json:"weight"`
}

// MapPointWeights is the normalized land-only B-spline field drawn by the app.
func MapPointWeights(latitude, longitude float64, valid func(int) bool) []PointWeight {
	if math.IsNaN(latitude) || math.IsNaN(longitude) || math.IsInf(latitude, 0) || math.IsInf(longitude, 0) || latitude < -60 || latitude > 90 {
		return nil
	}
	wrap := func(value, width int) int { return (value%width + width) % width }
	x, y := math.Mod(math.Mod((longitude+180)*4, 1440)+1440, 1440), (90-latitude)*4
	weights := func(fraction float64) [4]float64 {
		return [4]float64{math.Pow(1-fraction, 3) / 6, (3*math.Pow(fraction, 3) - 6*math.Pow(fraction, 2) + 4) / 6, (-3*math.Pow(fraction, 3) + 3*math.Pow(fraction, 2) + 3*fraction + 1) / 6, math.Pow(fraction, 3) / 6}
	}
	horizontal, vertical := weights(x-math.Floor(x)), weights(y-math.Floor(y))
	points := make([]PointWeight, 0, 16)
	landWeight := 0.0
	for row := range 4 {
		for column := range 4 {
			index := max(0, min(720, int(math.Floor(y))+row-1))*1440 + wrap(int(math.Floor(x))+column-1, 1440)
			if !valid(index) {
				continue
			}
			weight := horizontal[column] * vertical[row]
			points = append(points, PointWeight{index, weight})
			landWeight += weight
		}
	}
	for i := range points {
		points[i].Weight /= landWeight
	}
	return points
}

// BlendPlaceNumbers preserves the map's spatial model for every named forecast.
func BlendPlaceNumbers(points []WeightedNumbers) Numbers {
	if len(points) == 0 {
		return Numbers{}
	}
	result := Numbers{TotalDays: points[0].Numbers.TotalDays, SampleDays: points[0].Numbers.SampleDays, Parts: map[string]float64{}, Typical: map[string]float64{}}
	for _, point := range points {
		n, weight := point.Numbers, point.Weight
		result.MeanScore += n.MeanScore * weight
		result.NiceDayShare += n.NiceDayShare * weight
		result.PerfectDayShare += n.PerfectDayShare * weight
		result.AwfulDayShare += n.AwfulDayShare * weight
		result.NiceDayCount += n.NiceDayCount * weight
		result.PerfectDayCount += n.PerfectDayCount * weight
		result.AwfulDayCount += n.AwfulDayCount * weight
		for key, value := range n.Parts {
			result.Parts[key] += value * weight
		}
		for key, value := range n.Typical {
			result.Typical[key] += value * weight
		}
	}
	result.MeanScore = canonical(result.MeanScore)
	result.Score = result.MeanScore
	result.Band = bands[0]
	for i := range bands {
		if result.Score >= thresholds[i] {
			result.Band = bands[i]
		}
	}
	return result
}
