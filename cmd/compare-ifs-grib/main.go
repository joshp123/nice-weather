// compare-ifs-grib compares every native decoded sample with ecCodes on real
// ECMWF GRIB messages. ecCodes is needed only for this development check.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"nice-weather-finder/internal/providers/ifsgrib"
)

func main() {
	if err := compare(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func compare(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("usage: compare-ifs-grib message.grib2 [...]")
	}
	directory, err := os.MkdirTemp("", "ifs-eccodes-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	filter := filepath.Join(directory, "values.filter")
	if err = os.WriteFile(filter, []byte("print \"[values!1%0.17g]\";\n"), 0600); err != nil {
		return err
	}
	var total int
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		field, err := ifsgrib.Decode(payload)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		command := exec.Command("grib_filter", filter, path)
		command.Stderr = os.Stderr
		output, err := command.StdoutPipe()
		if err != nil {
			return err
		}
		if err = command.Start(); err != nil {
			return err
		}
		scanner := bufio.NewScanner(output)
		count := 0
		differences := 0
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			reference, parseErr := strconv.ParseFloat(text, 64)
			if parseErr != nil {
				return parseErr
			}
			if count >= len(field.Values) {
				return fmt.Errorf("ecCodes returned extra samples in %s", path)
			}
			if reference != field.Values[count] {
				if differences < 3 {
					fmt.Fprintf(os.Stderr, "%s sample %d: native %.17g ecCodes %.17g\n", path, count, field.Values[count], reference)
				}
				differences++
			}
			count++
		}
		if err = scanner.Err(); err != nil {
			return err
		}
		if err = command.Wait(); err != nil {
			return err
		}
		if count != len(field.Values) || differences != 0 {
			return fmt.Errorf("%s: %d/%d samples, %d differences", path, count, len(field.Values), differences)
		}
		total += count
		fmt.Printf("%s: %s step %d; %d values exactly equal to ecCodes\n", filepath.Base(path), field.Name, field.EndStep, count)
	}
	fmt.Printf("%d messages; %d values; zero differences\n", len(paths), total)
	return nil
}
