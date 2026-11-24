//go:build windows

package service

import "os"

func flockExclusiveNonBlocking(_ *os.File) error { return nil }
func flockUnlock(_ *os.File) error               { return nil }
func diskAvailableBytes(_ string) (int64, error) { return 1 << 40, nil }
