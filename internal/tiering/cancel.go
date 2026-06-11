package tiering

import (
	"context"
	"errors"
)

func isContextCanceled(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx == nil {
		return false
	}
	return errors.Is(ctx.Err(), context.Canceled)
}
