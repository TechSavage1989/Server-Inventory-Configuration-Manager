//go:build !windows

package main

import (
	"context"
	"errors"
)

func vddkPlatformOpen(ctx context.Context, folder string, params vddkConnect) (vddkSession, error) {
	return nil, errors.New("VDDK reads run only on Windows")
}
