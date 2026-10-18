// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Package placenumbers is the Go port of hosting/sites/fine-place.mjs.
package placenumbers

import (
	"fmt"
	"math"
	"strconv"
)

type Taste struct {
	WarmShift     float64 `json:"warmShift"`
	HeatShift     float64 `json:"heatShift"`
	EveningShift  float64 `json:"eveningShift"`
	HumidityShift float64 `json:"humidityShift"`
	WindShift     float64 `json:"windShift"`
	RainScale     float64 `json:"rainScale"`
}

var DefaultTaste = Taste{RainScale: 1}
var MonthDays = [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
var thresholds = [...]float64{0, 25, 40, 55, 70, 85, 100}
var bands = [...]string{"Awful", "Poor", "Fair", "Nice", "Beautiful", "Perfect"}

type curve [][2]float64

var cold = curve{{9, 0}, {11, 3}, {13, 10}, {15, 22}, {17, 40}, {19, 62}, {21, 85}, {23, 100}}
var heat = curve{{28, 100}, {31, 85}, {34, 60}, {37, 30}, {40, 10}, {43, 0}}
var evening = curve{{4, 0}, {8, 30}, {12, 65}, {16, 90}, {19, 100}}
var rain = curve{{0, 1}, {.5, .95}, {1, .88}, {2, .74}, {4, .5}, {8, .22}, {15, .05}}
var wind = curve{{15, 1}, {20, .97}, {25, .92}, {30, .85}, {40, .65}, {50, .4}, {65, .15}}
var humidity = curve{{16, 1}, {18, .95}, {20, .85}, {22, .65}, {24, .4}, {26, .2}, {28, .1}}
var sun = curve{{0, .75}, {.3, .85}, {.6, .95}, {.8, 1}}

func (c curve) at(x float64) float64 {
	if x <= c[0][0] {
		return c[0][1]
	}
	for i := 1; i < len(c); i++ {
		if x <= c[i][0] {
			a, b := c[i-1], c[i]
			return a[1] + (b[1]-a[1])*(x-a[0])/(b[0]-a[0])
		}
	}
	return c[len(c)-1][1]
}
func canonical(x float64) float64 { return math.Floor(x*10000+.5) / 10000 }
func vapor(x float64) float64     { return 6.105 * math.Exp(17.27*x/(237.7+x)) }

type PhysicalResponse struct {
	Dates        []string  `json:"dates"`
	Values       []float64 `json:"values"`
	Monthly      []float64 `json:"monthly"`
	ClockOffsets []float64 `json:"clockOffsets"`
}

type Replay struct {
	PhysicalResponse *PhysicalResponse `json:"physicalResponse"`
	FineIndex        int               `json:"fineIndex"`
	Dates            []string          `json:"dates"`
	SelectedIndices  []int             `json:"selectedIndices"`
	HighC            []float64         `json:"highC"`
	LowC             []float64         `json:"lowC"`
	AirC             []float64         `json:"airC"`
	DewC             []float64         `json:"dewC"`
	SunC             []float64         `json:"sunC"`
	ShadeC           []float64         `json:"shadeC"`
	RainMm           []float64         `json:"rainMm"`
	WindKmh          []float64         `json:"windKmh"`
	SunnyHours       []float64         `json:"sunnyHours"`
	DaylightHours    []float64         `json:"daylightHours"`
}
type Packet struct {
	Source       string       `json:"source"`
	Replay       Replay       `json:"replay"`
	Corrections  [][2]float64 `json:"corrections"`
	EveningModel string       `json:"eveningModel"`
	Records      [][]byte     `json:"records"`
	OffsetsC     []float64    `json:"offsetsC"`
	Weights      []float64    `json:"weights"`
}
type Period struct {
	Months    []int `json:"months"`
	TotalDays int   `json:"totalDays"`
}
type Numbers struct {
	MeanScore       float64            `json:"meanScore"`
	Score           float64            `json:"score"`
	Band            string             `json:"band"`
	NiceDayShare    float64            `json:"niceDayShare"`
	PerfectDayShare float64            `json:"perfectDayShare"`
	AwfulDayShare   float64            `json:"awfulDayShare"`
	NiceDayCount    float64            `json:"niceDayCount"`
	PerfectDayCount float64            `json:"perfectDayCount"`
	AwfulDayCount   float64            `json:"awfulDayCount"`
	TotalDays       int                `json:"totalDays"`
	SampleDays      int                `json:"sampleDays"`
	Parts           map[string]float64 `json:"parts"`
	Typical         map[string]float64 `json:"typical"`
}
type day struct {
	air, sun, shade                             [12]float64
	dew, nativeDew, rain, wind, sunny, daylight float64
}

func dateMonth(date string) int { month, _ := strconv.Atoi(date[5:7]); return month }
func correctedDay(p Packet, index int) day {
	r := p.Replay
	month := dateMonth(r.Dates[index])
	offsets := p.Corrections[month-1]
	hi, lo := r.HighC[index], r.LowC[index]
	width := hi - lo
	ordered := width > 0 && width+offsets[0]-offsets[1] > 0
	d := day{rain: r.RainMm[index], wind: r.WindKmh[index], sunny: r.SunnyHours[index], daylight: math.Max(1, r.DaylightHours[index])}
	for h := range 12 {
		at := index*12 + h
		t, td := r.AirC[at], r.DewC[at]
		delta := (offsets[0] + offsets[1]) / 2
		if ordered {
			delta = offsets[1] + (offsets[0]-offsets[1])*(t-lo)/width
		}
		if h == 11 && p.EveningModel == "uniform" {
			delta = (offsets[0] + offsets[1]) / 2
		}
		if h == 11 && p.EveningModel == "native" {
			delta = 0
		}
		d.air[h] = t + delta
		d.dew += math.Min(td, t+delta) / 12
		d.nativeDew += td / 12
		felt := delta + .33*(vapor(math.Min(td, t+delta))-vapor(math.Min(td, t)))
		d.sun[h] = r.SunC[at] + felt
		d.shade[h] = r.ShadeC[at] + felt
	}
	return d
}
func judge(d day, t Taste) (float64, map[string]float64) {
	daytime := 0.0
	for h := 0; h < int(d.daylight); h++ {
		daytime += math.Min(cold.at(d.sun[h]-t.WarmShift), heat.at(d.shade[h]+t.HeatShift))
	}
	walk := math.Min(evening.at(d.shade[11]-t.EveningShift), heat.at(d.shade[11]+t.HeatShift))
	warmth := (daytime + 2*walk) / (d.daylight + 2)
	parts := map[string]float64{"warmth": warmth, "evening": walk, "rainFactor": rain.at(d.rain * t.RainScale), "windFactor": wind.at(d.wind - t.WindShift), "humidityFactor": humidity.at(d.dew - t.HumidityShift), "sunFactor": sun.at(d.sunny / d.daylight)}
	return warmth * parts["rainFactor"] * parts["windFactor"] * parts["humidityFactor"] * parts["sunFactor"], parts
}
func typicalScore(shares [7]float64) float64 {
	for b := 5; b >= 0; b-- {
		if shares[b] < .5-1e-9 {
			continue
		}
		if b == 5 {
			return canonical(math.Max(85, 85+(shares[5]-.5)*30))
		}
		return canonical(math.Max(thresholds[b], thresholds[b]+(thresholds[b+1]-thresholds[b])*(shares[b]-.5)/(shares[b]-shares[b+1])))
	}
	return 0
}
func decodedForecastDay(record []byte, shift float64) day {
	d := day{dew: float64(record[10])/3 - 30, rain: math.Pow(float64(record[11])/50, 2), wind: float64(record[12]) / 2, sunny: float64(record[13]), daylight: math.Max(1, float64(record[14]))}
	sun := make(curve, 6)
	shade := make(curve, 4)
	for i := range sun {
		sun[i] = [2]float64{float64(i * 2), float64(record[i])/3 - 30 + shift}
	}
	for i := range shade {
		shade[i] = [2]float64{float64(i*3 + 2), float64(record[i+6])/3 - 30 + shift}
	}
	for h := range 12 {
		d.sun[h] = sun.at(float64(h))
		d.shade[h] = shade.at(float64(h))
	}
	return d
}

// PlaceNumbers is the single Go entry point for named-place climate and forecast judgements.
// Forecast uses existing encoded weather and score-level interpolation; it adds no climate correction.
func PlaceNumbers(p Packet, period Period, taste *Taste) (Numbers, error) {
	t := DefaultTaste
	if taste != nil {
		t = *taste
	}
	n := Numbers{Parts: map[string]float64{}, Typical: map[string]float64{}}
	var shares [7]float64
	single := 0.0
	add := func(d day, score float64, parts map[string]float64, weight float64) {
		n.SampleDays++
		single = score
		for i, v := range thresholds {
			if score >= v {
				shares[i] += weight
			}
		}
		for k, v := range parts {
			n.Parts[k] += v * weight
		}
		values := map[string]float64{"feelsInSunAt14C": d.sun[4], "feelsInShadeAt15C": d.shade[5], "feelsInShadeAt21C": d.shade[11], "dewPointC": d.dew, "rainMmPerDay": d.rain, "wetDayShare": 0, "windKmh": d.wind, "sunnyShare": d.sunny / d.daylight, "daylightHours": d.daylight}
		if d.rain >= 1 {
			values["wetDayShare"] = 1
		}
		for k, v := range values {
			n.Typical[k] += v * weight
		}
	}
	if p.Source == "forecast" {
		if len(p.Records) != len(p.OffsetsC) || len(p.Records) != len(p.Weights) {
			return n, fmt.Errorf("forecast record, offset and weight counts differ")
		}
		sum := 0.0
		for _, weight := range p.Weights {
			sum += weight
		}
		n.TotalDays = int(math.Round(sum))
		for i, record := range p.Records {
			if len(record) != 15 {
				return n, fmt.Errorf("forecast record has %d bytes", len(record))
			}
			weight := p.Weights[i] / sum
			level := math.Max(0, math.Min(10, (p.OffsetsC[i]+15)/3))
			lower := math.Min(math.Floor(level), 9)
			fraction := level - lower
			left, right := decodedForecastDay(record, -15+lower*3), decodedForecastDay(record, -15+(lower+1)*3)
			a, pa := judge(left, t)
			b, pb := judge(right, t)
			score := canonical(float64(a*(1-fraction)) + float64(b*fraction))
			parts := map[string]float64{}
			for k, v := range pa {
				parts[k] = v*(1-fraction) + pb[k]*fraction
			}
			d := decodedForecastDay(record, p.OffsetsC[i])
			add(d, score, parts, weight)
		}
	} else {
		for _, month := range period.Months {
			if month < 1 || month > 12 {
				return n, fmt.Errorf("invalid month")
			}
			n.TotalDays += MonthDays[month-1]
		}
		for _, month := range period.Months {
			indices := []int{}
			for _, i := range p.Replay.SelectedIndices {
				if dateMonth(p.Replay.Dates[i]) == month {
					indices = append(indices, i)
				}
			}
			if len(indices) == 0 {
				return n, fmt.Errorf("no representative days for month %d", month)
			}
			weight := float64(MonthDays[month-1]) / float64(n.TotalDays) / float64(len(indices))
			for _, i := range indices {
				d := correctedDay(p, i)
				score, parts := judge(d, t)
				add(d, canonical(score), parts, weight)
			}
		}
	}
	n.Score = typicalScore(shares)
	if n.TotalDays == 1 {
		n.Score = single
	}
	n.MeanScore = n.Score
	n.Band = bands[0]
	for i := range bands {
		if n.Score >= thresholds[i] {
			n.Band = bands[i]
		}
	}
	n.NiceDayShare = shares[3]
	n.PerfectDayShare = shares[5]
	n.AwfulDayShare = math.Max(0, 1-shares[1])
	count := func(share float64) float64 { return math.Floor(share*float64(n.TotalDays)*1e9+.5) / 1e9 }
	n.NiceDayCount = count(n.NiceDayShare)
	n.PerfectDayCount = count(n.PerfectDayShare)
	n.AwfulDayCount = count(n.AwfulDayShare)
	return n, nil
}
