// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
package outlook

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type namedForecastTable struct {
	body  []byte
	rows  map[[2]int32]int
	count int
}

func loadNamedForecast(ctx context.Context, source, run string) (*namedForecastTable, error) {
	name := "v6/forecast/" + run + "/named.bin.gz"
	var body []byte
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source, "/")+"/"+name, nil)
		response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("named forecast: HTTP %d", response.StatusCode)
		}
		body, err = io.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		body, err = os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return decodeNamedForecast(body)
}
func decodeNamedForecast(body []byte) (*namedForecastTable, error) {
	if len(body) > 2 && body[0] == 31 && body[1] == 139 {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		body, err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(body) < 32 || string(body[:4]) != "NWNP" || binary.LittleEndian.Uint16(body[4:]) != 1 || body[6] != 2 || body[7] != 15 {
		return nil, fmt.Errorf("invalid named forecast header")
	}
	count := int(binary.LittleEndian.Uint32(body[8:]))
	if len(body) != 32+count*8+count*15*15 {
		return nil, fmt.Errorf("invalid named forecast length")
	}
	rows := make(map[[2]int32]int, count)
	for row := range count {
		at := 32 + row*8
		rows[[2]int32{int32(binary.LittleEndian.Uint32(body[at:])), int32(binary.LittleEndian.Uint32(body[at+4:]))}] = row
	}
	return &namedForecastTable{body: body, rows: rows, count: count}, nil
}
func (table *namedForecastTable) apply(places []Place, slot int) {
	for index := range places {
		place := &places[index]
		key := [2]int32{int32(math.Floor(place.Latitude*1e5 + .5)), int32(math.Floor(place.Longitude*1e5 + .5))}
		row, found := table.rows[key]
		if !found {
			continue
		}
		start := 32 + table.count*8 + slot*table.count*15
		at := start + row*3
		encoded := uint32(table.body[at]) | uint32(table.body[at+1])<<8 | uint32(table.body[at+2])<<16
		if encoded == 0xffffff {
			continue
		}
		place.FloatScore = float64(encoded) / 10000
		place.Score = int(math.Floor(place.FloatScore))
		shares := [3]float64{}
		for field := range 3 {
			shares[field] = float64(math.Float32frombits(binary.LittleEndian.Uint32(table.body[start+table.count*3+(field*table.count+row)*4:])))
		}
		days := 1.
		if slot == 14 {
			days = 14
		}
		place.NiceDayCount = shares[0] * days
		place.PerfectDayCount = shares[1] * days
	}
}
