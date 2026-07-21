package update

import (
	"context"

	"nice-weather-finder/internal/climatebuild"
)

type climateRetryFunc func(context.Context, func() error) error

type globalClimateRetrySource struct {
	climatebuild.Source
	retry climateRetryFunc
}

func newGlobalClimateRetrySource(source climatebuild.Source) climatebuild.Source {
	return globalClimateRetrySource{Source: source, retry: withRetry}
}

func (source globalClimateRetrySource) Fetch(ctx context.Context, reference climatebuild.ChunkReference) (climatebuild.Block, error) {
	var block climatebuild.Block
	err := source.retry(ctx, func() error {
		var err error
		block, err = source.Source.Fetch(ctx, reference)
		return err
	})
	return block, err
}
