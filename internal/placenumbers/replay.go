// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
package placenumbers

import (
	"encoding/binary"
	"fmt"
	"math"
)

type MonthFacts struct {
	Month          int     `json:"month"`
	HighC          float64 `json:"highC"`
	FeelsAt21C     float64 `json:"feelsAt21C"`
	DewPointC      float64 `json:"dewPointC"`
	WetDaysPercent float64 `json:"wetDaysPercent"`
	SunnyPercent   float64 `json:"sunnyPercent"`
	WindKmh        float64 `json:"windKmh"`
	Score          float64 `json:"score"`
	NiceDayShare   float64 `json:"niceDayShare"`
}

// DecodeReplay reads the same NWFP v1 body as the JavaScript loader. Corrections
// supplied to PlaceNumbers must already include the per-month clock offsets.
func DecodeReplay(body []byte, dates []string) (Replay, error) {
	r := Replay{}
	if len(body) < 32 || string(body[:4]) != "NWFP" || body[4] != 1 {
		return r, fmt.Errorf("invalid native replay header")
	}
	count, days := int(binary.LittleEndian.Uint16(body[6:])), int(binary.LittleEndian.Uint16(body[14:]))
	if days != len(dates) || len(body) != 32+count*2+count*54*4+days*6*4+12*4*4+12*2*4 {
		return r, fmt.Errorf("native replay shape differs from date axis")
	}
	r.FineIndex = int(binary.LittleEndian.Uint32(body[8:]))
	offset := 32
	indices := make([]int, count)
	for i := range count {
		indices[i] = int(binary.LittleEndian.Uint16(body[offset:]))
		offset += 2
		if indices[i] >= days {
			return r, fmt.Errorf("native replay date index out of range")
		}
		r.Dates = append(r.Dates, dates[indices[i]])
		r.SelectedIndices = append(r.SelectedIndices, i)
	}
	read := func() float64 {
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(body[offset:])))
		offset += 4
		return value
	}
	for range count {
		for range 12 {
			r.AirC = append(r.AirC, read())
		}
		for range 12 {
			r.DewC = append(r.DewC, read())
		}
		for range 12 {
			r.SunC = append(r.SunC, read())
		}
		for range 12 {
			r.ShadeC = append(r.ShadeC, read())
		}
		r.HighC = append(r.HighC, read())
		r.LowC = append(r.LowC, read())
		r.RainMm = append(r.RainMm, read())
		r.WindKmh = append(r.WindKmh, read())
		r.SunnyHours = append(r.SunnyHours, read())
		r.DaylightHours = append(r.DaylightHours, read())
	}
	physical := &PhysicalResponse{Dates: dates}
	for range days * 6 {
		physical.Values = append(physical.Values, read())
	}
	for range 12 * 4 {
		physical.Monthly = append(physical.Monthly, read())
	}
	for range 12 * 2 {
		physical.ClockOffsets = append(physical.ClockOffsets, read())
	}
	r.PhysicalResponse = physical
	return r, nil
}
func correctedAir(t, hi, lo float64, offsets [2]float64) float64 {
	width := hi - lo
	delta := (offsets[0] + offsets[1]) / 2
	if width > 0 && width+offsets[0]-offsets[1] > 0 {
		delta = offsets[1] + (offsets[0]-offsets[1])*(t-lo)/width
	}
	return t + delta
}
func PlaceMonths(packet Packet, taste *Taste) ([]MonthFacts, error) {
	results := make([]MonthFacts, 12)
	for month := 1; month <= 12; month++ {
		value := MonthFacts{Month: month}
		count := 0
		if response := packet.Replay.PhysicalResponse; response != nil {
			offsets := packet.Corrections[month-1]
			for i, date := range response.Dates {
				if dateMonth(date) != month {
					continue
				}
				at := i * 6
				hi, lo, tmax, t21, td21, shade := response.Values[at], response.Values[at+1], response.Values[at+2], response.Values[at+3], response.Values[at+4], response.Values[at+5]
				new21 := correctedAir(t21, hi, lo, offsets)
				if packet.EveningModel == "uniform" {
					new21 = t21 + (offsets[0]+offsets[1])/2
				}
				if packet.EveningModel == "native" {
					new21 = t21
				}
				value.HighC += correctedAir(tmax, hi, lo, offsets)
				value.FeelsAt21C += shade + new21 - t21 + .33*(vapor(math.Min(td21, new21))-vapor(math.Min(td21, t21)))
				count++
			}
			value.HighC /= float64(count)
			value.FeelsAt21C /= float64(count)
			at := (month - 1) * 4
			value.DewPointC = response.Monthly[at]
			value.WetDaysPercent = response.Monthly[at+1]
			value.SunnyPercent = response.Monthly[at+2]
			value.WindKmh = response.Monthly[at+3]
		} else {
			for i, date := range packet.Replay.Dates {
				if dateMonth(date) != month {
					continue
				}
				d := correctedDay(packet, i)
				high := d.air[0]
				for _, t := range d.air[:11] {
					high = math.Max(high, t)
				}
				value.HighC += high
				value.FeelsAt21C += d.shade[11]
				value.DewPointC += d.nativeDew
				if d.rain >= 1 {
					value.WetDaysPercent += 100
				}
				value.SunnyPercent += 100 * d.sunny / d.daylight
				value.WindKmh += d.wind
				count++
			}
			value.HighC /= float64(count)
			value.FeelsAt21C /= float64(count)
			value.DewPointC /= float64(count)
			value.WetDaysPercent /= float64(count)
			value.SunnyPercent /= float64(count)
			value.WindKmh /= float64(count)
		}
		if count == 0 {
			return nil, fmt.Errorf("no physical days for month %d", month)
		}
		numbers, err := PlaceNumbers(packet, Period{Months: []int{month}}, taste)
		if err != nil {
			return nil, err
		}
		value.Score = numbers.Score
		value.NiceDayShare = numbers.NiceDayShare
		results[month-1] = value
	}
	return results, nil
}
