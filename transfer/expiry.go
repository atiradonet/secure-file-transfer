package main

import (
	"fmt"
	"strconv"
	"time"
)

func parseExpiry(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid expiry %q: use a number followed by m/h/d (e.g. 30m, 4h, 1d)", s)
	}
	unit := s[len(s)-1]
	amount, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("invalid expiry %q: %w", s, err)
	}
	if amount <= 0 {
		return 0, fmt.Errorf("expiry must be a positive number")
	}
	var d time.Duration
	switch unit {
	case 'm':
		d = time.Duration(amount) * time.Minute
	case 'h':
		d = time.Duration(amount) * time.Hour
	case 'd':
		d = time.Duration(amount) * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid expiry unit %q: use m, h, or d", string(unit))
	}
	if d > 24*time.Hour {
		return 0, fmt.Errorf("expiry cannot exceed 24 hours — files are auto-deleted after 1 day")
	}
	return d, nil
}
