package climate

import (
	"testing"
	"time"

	"nice-weather-finder/internal/weather"
)

func TestAggregateObservationsUsesExpectedValidDateContinuity(t *testing.T) {
	t.Run("Apia skip stays contiguous", func(t *testing.T) {
		var expected []time.Time
		var observations []DailyObservation
		for day := 1; day <= 31; day++ {
			if day == 30 {
				continue
			}
			date := time.Date(2011, time.December, day, 0, 0, 0, 0, time.UTC)
			metrics := observationTestMetrics()
			if day >= 26 {
				metrics.ComfortableTempC = 0
				metrics.MinTempC = -2
				metrics.MaxTempC = 2
				metrics.DewPointC.Value = -2
			}
			expected = append(expected, date)
			observations = append(observations, DailyObservation{
				Date: date, Latitude: -13.83, Longitude: -171.75, Metrics: metrics,
			})
		}
		distribution, err := AggregateObservations(observations, expected)
		if err != nil {
			t.Fatal(err)
		}
		if distribution.Nice.LongestPoorRun != 5 {
			t.Fatalf("Apia longest poor run = %d; want 5 across 29->31", distribution.Nice.LongestPoorRun)
		}
		if distribution.Nice.SevenDayWindows != 24 || distribution.Nice.PoorWeekWindows != 3 {
			t.Fatalf("Apia seven-day evidence = %d windows, %d poor; want 24/3",
				distribution.Nice.SevenDayWindows, distribution.Nice.PoorWeekWindows)
		}
	})

	t.Run("same month does not bridge years", func(t *testing.T) {
		var expected []time.Time
		var observations []DailyObservation
		for _, year := range []int{2006, 2007} {
			for day := 1; day <= 31; day++ {
				date := time.Date(year, time.December, day, 0, 0, 0, 0, time.UTC)
				metrics := observationTestMetrics()
				if (year == 2006 && day >= 30) || (year == 2007 && day <= 2) {
					metrics.ComfortableTempC = 0
					metrics.MinTempC = -2
					metrics.MaxTempC = 2
					metrics.DewPointC.Value = -2
				}
				expected = append(expected, date)
				observations = append(observations, DailyObservation{
					Date: date, Latitude: 52, Longitude: 4, Metrics: metrics,
				})
			}
		}
		distribution, err := AggregateObservations(observations, expected)
		if err != nil {
			t.Fatal(err)
		}
		if distribution.Nice.LongestPoorRun != 2 {
			t.Fatalf("cross-year longest poor run = %d; want 2", distribution.Nice.LongestPoorRun)
		}
		if distribution.Nice.SevenDayWindows != 50 {
			t.Fatalf("cross-year seven-day windows = %d; want 50 without a year bridge", distribution.Nice.SevenDayWindows)
		}
	})
}

func observationTestMetrics() weather.DailyMetrics {
	return weather.DailyMetrics{
		ComfortableTempC: 24,
		MinTempC:         20,
		MaxTempC:         28,
		WetTimeFraction:  weather.OptionalFloat{Available: true},
		SolarEnergyMJm2:  weather.OptionalFloat{Value: 18, Available: true},
		CloudCoverPercent: weather.OptionalFloat{
			Value: 20, Available: true,
		},
		WindSpeedKPH: 8,
		DewPointC: weather.OptionalFloat{
			Value: 12, Available: true,
		},
		RelativeHumidityPercent: weather.OptionalFloat{
			Value: 45, Available: true,
		},
		NativeResolutionKM: 28,
		Valid:              true,
	}
}
