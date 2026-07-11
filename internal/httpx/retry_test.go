package httpx

import (
	"net/http"
	"testing"
	"time"
)

func TestShouldRetryStatus(t *testing.T) {
	retryable := []int{http.StatusRequestTimeout, http.StatusTooManyRequests, 500, 502, 503, 504}
	for _, s := range retryable {
		if !ShouldRetryStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	permanent := []int{http.StatusOK, http.StatusMovedPermanently, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}
	for _, s := range permanent {
		if ShouldRetryStatus(s) {
			t.Errorf("status %d should not be retryable", s)
		}
	}
}

func TestBackoff(t *testing.T) {
	base := 100 * time.Millisecond
	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{0, 0},
		{1, 100 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 900 * time.Millisecond},
	} {
		if got := Backoff(tc.attempt, base); got != tc.want {
			t.Errorf("Backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}
