package dailyforecast

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nice-weather-finder/internal/clearsky"
)

const v6Days = 14

type v6Point struct {
	fine, source, cell int
	shift              float64
	location           *time.Location
}
type v6Frame struct {
	at     time.Time
	fields [6][]float32
}
type v6Forecast struct {
	points []v6Point
	cells  [][2]uint16
	frames []v6Frame
}
type v6Pointer struct {
	Run         string `json:"run"`
	FirstDate   string `json:"firstDate"`
	Days        int    `json:"days"`
	PreviousRun string `json:"previousRun"`
}

func loadV6Static(ctx context.Context, source string) (*v6Forecast, error) {
	read := func(name string) ([]byte, error) {
		if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
			return os.ReadFile(filepath.Join(source, name))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source, "/")+"/"+name, nil)
		if err != nil {
			return nil, err
		}
		response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, fmt.Errorf("v6 %s: HTTP %d", name, response.StatusCode)
		}
		return io.ReadAll(io.LimitReader(response.Body, 16<<20))
	}
	cells, err := read("cells.bin")
	if err != nil {
		return nil, err
	}
	points, err := read("points.bin")
	if err != nil {
		return nil, err
	}
	if len(cells) == 0 || len(cells)%4 != 0 || len(points) < 4 || int(binary.LittleEndian.Uint32(points))*12+4 != len(points) {
		return nil, fmt.Errorf("invalid v6 static file sizes")
	}
	v := &v6Forecast{cells: make([][2]uint16, len(cells)/4), points: make([]v6Point, (len(points)-4)/12)}
	for i := range v.cells {
		v.cells[i] = [2]uint16{binary.LittleEndian.Uint16(cells[i*4:]), binary.LittleEndian.Uint16(cells[i*4+2:])}
		if v.cells[i][0] >= 180 || v.cells[i][1] >= 360 {
			return nil, fmt.Errorf("invalid v6 cell %d", i)
		}
	}
	fine := make([]int, len(v.points))
	for i := range v.points {
		b := points[4+i*12:]
		p := v6Point{fine: int(binary.LittleEndian.Uint32(b)), source: int(binary.LittleEndian.Uint32(b[4:])), shift: float64(int16(binary.LittleEndian.Uint16(b[8:]))) / 100, cell: int(binary.LittleEndian.Uint16(b[10:]))}
		if p.fine >= Width*Height || p.source >= Width*Height || p.cell >= len(v.cells) {
			return nil, fmt.Errorf("invalid v6 point %d", i)
		}
		v.points[i] = p
		fine[i] = p.fine
	}
	groups, err := timezoneGroups(fine)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		for _, i := range g.landIndices {
			v.points[i].location = g.location
		}
	}
	return v, nil
}

func (v *v6Forecast) addFrame(at time.Time, frames [][]float32) {
	frame := v6Frame{at: at}
	for f, native := range []int{0, 1, 3, 4, 5, 6} {
		frame.fields[f] = make([]float32, len(v.points))
		for i, p := range v.points {
			index := p.fine
			if f < 2 {
				index = p.source
			}
			frame.fields[f][i] = frames[native][index]
		}
	}
	v.frames = append(v.frames, frame)
}

func (v *v6Forecast) sample(point, field int, at time.Time) float64 {
	right := sort.Search(len(v.frames), func(i int) bool { return !v.frames[i].at.Before(at) })
	if right == 0 {
		return float64(v.frames[0].fields[field][point])
	}
	if right == len(v.frames) {
		return math.NaN()
	}
	left := right - 1
	weight := at.Sub(v.frames[left].at).Seconds() / v.frames[right].at.Sub(v.frames[left].at).Seconds()
	return float64(v.frames[left].fields[field][point])*(1-weight) + float64(v.frames[right].fields[field][point])*weight
}

func v6Daylight(date time.Time, p v6Point) float64 {
	lat := (90 - float64(p.fine/Width)*.25) * math.Pi / 180
	lon := -180 + float64(p.fine%Width)*.25
	gamma := 2 * math.Pi / 365 * (float64(date.YearDay()) - 1)
	equation := 229.18 * (.000075 + .001868*math.Cos(gamma) - .032077*math.Sin(gamma) - .014615*math.Cos(2*gamma) - .040849*math.Sin(2*gamma))
	declination := .006918 - .399912*math.Cos(gamma) + .070257*math.Sin(gamma) - .006758*math.Cos(2*gamma) + .000907*math.Sin(2*gamma) - .002697*math.Cos(3*gamma) + .00148*math.Sin(3*gamma)
	argument := math.Cos(90.833*math.Pi/180)/(math.Cos(lat)*math.Cos(declination)) - math.Tan(lat)*math.Tan(declination)
	if argument >= 1 {
		return 1
	}
	if argument <= -1 {
		return 11
	}
	sunset := (720 - 4*lon - equation + 4*math.Acos(max(-1, min(1, argument)))*180/math.Pi) / 60
	_, offset := time.Date(date.Year(), date.Month(), date.Day(), 12, 0, 0, 0, p.location).Zone()
	return max(1, min(11, math.Ceil(sunset+float64(offset)/3600)-10))
}

func v6Encode(values [15]float64) (record [15]byte) {
	for i, x := range values {
		switch {
		case i <= 10:
			x = (x + 30) * 3
		case i == 11:
			x = math.Sqrt(max(0, x)) * 50
		case i == 12:
			x *= 2
		}
		record[i] = byte(max(0, min(255, math.Round(x))))
	}
	return
}

type v6Diagnostic struct {
	Name      string  `json:"name"`
	Cell      int     `json:"cell"`
	Fine      int     `json:"fine"`
	Date      string  `json:"date"`
	Air14     float64 `json:"air14"`
	Dew       float64 `json:"dew"`
	Rain      float64 `json:"rain"`
	Wind      float64 `json:"wind"`
	FineAir14 float64 `json:"fineAir14"`
	FineRain  float64 `json:"fineRain"`
	FineWind  float64 `json:"fineWind"`
}

func (v *v6Forecast) write(ctx context.Context, output string, run time.Time, runID string, log io.Writer) error {
	started := time.Now()
	directory := filepath.Join(output, "v6", "forecast", runID)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	byCell := make([][]int, len(v.cells))
	for i, p := range v.points {
		byCell[p.cell] = append(byCell[p.cell], i)
	}
	records := make([]byte, len(v.cells)*v6Days*15)
	ephemeris := make([]clearsky.ForecastHour, 361)
	for h := 1; h <= 360; h++ {
		ephemeris[h] = clearsky.PrepareForecastHour(run.Add(time.Duration(h) * time.Hour))
	}
	type localHour struct {
		at    time.Time
		solar clearsky.ForecastHour
	}
	schedules := map[*time.Location]*[v6Days][12]localHour{}
	for _, p := range v.points {
		if _, exists := schedules[p.location]; exists {
			continue
		}
		schedule := new([v6Days][12]localHour)
		for day := 0; day < v6Days; day++ {
			date := run.AddDate(0, 0, day)
			for h := 0; h < 12; h++ {
				at := time.Date(date.Year(), date.Month(), date.Day(), 10+h, 0, 0, 0, p.location)
				schedule[day][h] = localHour{at, clearsky.PrepareForecastHour(at.Add(time.Hour))}
			}
		}
		schedules[p.location] = schedule
	}
	type proofPlace struct {
		name                string
		latitude, longitude float64
	}
	places := []proofPlace{{"Málaga", 36.72, -4.42}, {"Rotterdam", 51.92, 4.48}, {"Singapore", 1.29, 103.85}, {"San Diego", 32.72, -117.16}}
	proofCell := map[int]int{}
	proofPoint := map[int]int{}
	for placeIndex, place := range places {
		for cell, pair := range v.cells {
			if int(pair[0]) == int(math.Floor(90-place.latitude)) && int(pair[1]) == int(math.Floor(place.longitude+180)) {
				proofCell[cell] = placeIndex
				distance := math.Inf(1)
				for _, i := range byCell[cell] {
					p := v.points[i]
					lat := 90 - float64(p.fine/Width)*.25
					lon := -180 + float64(p.fine%Width)*.25
					d := math.Pow(lat-place.latitude, 2) + math.Pow((lon-place.longitude)*math.Cos(lat*math.Pi/180), 2)
					if d < distance {
						distance = d
						proofPoint[cell] = i
					}
				}
			}
		}
	}
	diagnostics := make([][]v6Diagnostic, len(v.cells))
	var next, completed atomic.Int64
	var wg sync.WaitGroup
	var failure error
	var failureOnce sync.Once
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				cell := int(next.Add(1) - 1)
				if cell >= len(v.cells) || ctx.Err() != nil {
					return
				}
				if len(byCell[cell]) == 0 {
					failureOnce.Do(func() { failure = fmt.Errorf("v6 cell %d has no points", cell) })
					continue
				}
				var sums [v6Days][15]float64
				var air14 [v6Days]float64
				var fineProof [v6Days][3]float64
				for _, pointIndex := range byCell[cell] {
					p := v.points[pointIndex]
					solar, err := clearsky.New(90-float64(p.fine/Width)*.25, -180+float64(p.fine%Width)*.25, 0)
					if err != nil {
						failureOnce.Do(func() { failure = err })
						return
					}
					clearGlobal := make([]float64, 361)
					clearNormalByHour := make([]float64, 361)
					intervalClear := make([]float64, len(v.frames))
					for step := 1; step < len(v.frames); step++ {
						first := int(v.frames[step-1].at.Sub(run)/time.Hour) + 1
						last := int(v.frames[step].at.Sub(run) / time.Hour)
						for h := first; h <= last; h++ {
							clearGlobal[h], clearNormalByHour[h] = solar.ForecastRadiation(ephemeris[h])
							intervalClear[step] += clearGlobal[h]
						}
						intervalClear[step] /= float64(last - first + 1)
					}
					for day := 0; day < v6Days; day++ {
						date := run.AddDate(0, 0, day)
						var values [15]float64
						values[14] = v6Daylight(date, p)
						local := schedules[p.location][day][0].at
						values[11] = max(0, (v.sample(pointIndex, 4, local.Add(11*time.Hour))-v.sample(pointIndex, 4, local))*1000)
						var pointAir14 float64
						for h := 0; h < 12; h++ {
							at := schedules[p.location][day][h].at
							temperature := v.sample(pointIndex, 0, at) - 273.15 + p.shift
							dew := min(temperature, v.sample(pointIndex, 1, at)-273.15+p.shift)
							wind := math.Hypot(v.sample(pointIndex, 2, at), v.sample(pointIndex, 3, at))
							values[10] += dew / 12
							values[12] += wind * 3.6 / 12
							if h == 4 {
								pointAir14 = temperature
								air14[day] += temperature
							}
							hourEnd := at.Add(time.Hour)
							hourIndex := hourEnd.Sub(run) / time.Hour
							clearNormal := 0.0
							if hourEnd.Sub(run)%time.Hour == 0 && hourIndex > 0 && hourIndex <= 360 {
								clearNormal = clearNormalByHour[hourIndex]
							} else {
								_, clearNormal = solar.ForecastRadiation(schedules[p.location][day][h].solar)
							}
							dni := 0.0
							firstInterval := max(1, sort.Search(len(v.frames), func(i int) bool { return v.frames[i].at.After(at) }))
							for step := firstInterval; step < len(v.frames) && v.frames[step-1].at.Before(hourEnd); step++ {
								start, end := v.frames[step-1].at, v.frames[step].at
								overlap := minTime(hourEnd, end).Sub(maxTime(at, start)).Hours()
								if overlap <= 0 {
									continue
								}
								radiation := max(0, float64(v.frames[step].fields[5][pointIndex]-v.frames[step-1].fields[5][pointIndex])) / end.Sub(start).Seconds()
								dni += clearsky.EstimateForecastDNI(radiation, intervalClear[step], clearNormal) * overlap
							}
							shade := temperature + .33*6.105*math.Exp(17.27*dew/(237.7+dew)) - .70*wind - 4
							sun := shade + .105*dni/(wind+10)
							if h <= 10 && h%2 == 0 {
								values[h/2] = sun
							}
							switch h {
							case 2:
								values[6] = shade
							case 5:
								values[7] = shade
							case 8:
								values[8] = shade
							case 11:
								values[9] = shade
							}
							if float64(h) < values[14] && dni >= 300 {
								values[13]++
							}
						}
						for field, x := range values {
							if !finite(x) {
								failureOnce.Do(func() { failure = fmt.Errorf("v6 nonfinite point %d day %d field %d", p.fine, day, field) })
								return
							}
							sums[day][field] += x
						}
						if pointIndex == proofPoint[cell] {
							fineProof[day] = [3]float64{pointAir14, values[11], values[12]}
						}
					}
				}
				for day := 0; day < v6Days; day++ {
					for f := range sums[day] {
						sums[day][f] /= float64(len(byCell[cell]))
					}
					record := v6Encode(sums[day])
					copy(records[(cell*v6Days+day)*15:], record[:])
					if placeIndex, ok := proofCell[cell]; ok {
						diagnostics[cell] = append(diagnostics[cell], v6Diagnostic{places[placeIndex].name, cell, v.points[proofPoint[cell]].fine, run.AddDate(0, 0, day).Format("2006-01-02"), air14[day] / float64(len(byCell[cell])), sums[day][10], sums[day][11], sums[day][12], fineProof[day][0], fineProof[day][1], fineProof[day][2]})
					}
				}
				if n := completed.Add(1); n%2000 == 0 {
					fmt.Fprintf(log, "v6: %d/%d cells in %s\n", n, len(v.cells), time.Since(started).Round(time.Second))
				}
			}
		}()
	}
	wg.Wait()
	if failure != nil {
		return failure
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "days.bin"), records, 0644); err != nil {
		return err
	}
	var proof []v6Diagnostic
	for _, d := range diagnostics {
		proof = append(proof, d...)
	}
	body, _ := json.MarshalIndent(proof, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, "comparison.json"), body, 0644); err != nil {
		return err
	}
	pointerPath := filepath.Join(output, "v6", "forecast", "current.json")
	var previous v6Pointer
	if body, err := os.ReadFile(pointerPath); err == nil {
		if err = json.Unmarshal(body, &previous); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	pointer := v6Pointer{runID, run.Format("2006-01-02"), v6Days, previous.Run}
	if previous.Run == runID {
		pointer.PreviousRun = previous.PreviousRun
	}
	body, _ = json.Marshal(pointer)
	if err := os.WriteFile(pointerPath+".partial", append(body, '\n'), 0644); err != nil {
		return err
	}
	if err := os.Rename(pointerPath+".partial", pointerPath); err != nil {
		return err
	}
	fmt.Fprintf(log, "v6: %d points; %d cells; %d days; %d bytes; %s\n", len(v.points), len(v.cells), v6Days, len(records), time.Since(started).Round(time.Millisecond))
	return nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
