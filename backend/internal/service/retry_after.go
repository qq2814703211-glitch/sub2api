package service

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfter parses the Retry-After response header (delta-seconds or HTTP
// date) into a non-negative delay measured from now. Zero means "not given or
// already elapsed", which callers treat as "no hint".
func retryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
