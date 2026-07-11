// Package httpx holds the small HTTP retry policy shared by every network
// client in bunny (update checker, remote catalog, binary downloader) so the
// definition of "what is worth retrying" cannot drift between them.
package httpx

import (
	"net/http"
	"time"
)

// ShouldRetryStatus reports whether an HTTP status code warrants a retry:
// a request timeout, rate limiting, or any 5xx server error. Other 4xx
// responses are treated as permanent.
func ShouldRetryStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

// Backoff returns the delay before retry attempt n (1-based) as a quadratic
// function of the attempt number: base * n^2. Callers pick base to suit the
// payload (a small metadata fetch can retry sooner than a large download).
func Backoff(attempt int, base time.Duration) time.Duration {
	return time.Duration(attempt*attempt) * base
}
