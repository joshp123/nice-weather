package scoring

import "nice-weather-finder/internal/weatherinput"

func interpolate(value float64, points [][2]float64) float64 {
	if value <= points[0][0] {
		return points[0][1]
	}
	last := points[len(points)-1]
	if value >= last[0] {
		return last[1]
	}
	for i := 0; i < len(points)-1; i++ {
		left := points[i]
		right := points[i+1]
		if value >= left[0] && value <= right[0] {
			span := right[0] - left[0]
			if span == 0 {
				return right[1]
			}
			fraction := (value - left[0]) / span
			return left[1] + fraction*(right[1]-left[1])
		}
	}
	return 0
}

func ComponentScores(input weatherinput.WeatherInput) Components {
	return Components{
		Temp:     interpolate(input.TempC, [][2]float64{{8, 0}, {12, 20}, {16, 45}, {18, 70}, {20, 88}, {22, 100}, {27, 100}, {30, 85}, {33, 55}, {36, 20}, {39, 0}}),
		Rain:     interpolate(input.RainMM, [][2]float64{{0, 100}, {0.5, 90}, {2, 65}, {5, 30}, {10, 0}}),
		Cloud:    interpolate(input.CloudPct, [][2]float64{{0, 100}, {20, 100}, {40, 85}, {60, 60}, {80, 30}, {100, 5}}),
		Humidity: interpolate(input.Humidity, [][2]float64{{0, 70}, {30, 100}, {60, 100}, {70, 80}, {80, 50}, {90, 15}, {100, 0}}),
		Wind:     interpolate(input.WindKMH, [][2]float64{{0, 100}, {10, 100}, {20, 80}, {30, 45}, {40, 15}, {50, 0}}),
	}
}
