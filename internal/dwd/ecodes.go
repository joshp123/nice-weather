package dwd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

func ReadGribValues(ctx context.Context, path string) ([]GridValue, error) {
	command := exec.CommandContext(ctx, "grib_get_data", path)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("grib_get_data %s failed: %w: %s", path, err, stderr.String())
	}
	return ParseGribGetData(bytes.NewReader(output))
}
