package transporterr

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Upstream retains retry evidence without retaining a URL, response body, or
// wrapped network error that might disclose credentials or internal hosts.
type Upstream struct {
	Message    string
	Status     int
	RetryAfter time.Duration
	Transient  bool
}

func (e *Upstream) Error() string { return e.Message }

func HTTP(message string, response *http.Response) error {
	delay := time.Duration(0)
	if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 32); err == nil && seconds > 0 {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && time.Until(at) > 0 {
		delay = time.Until(at)
	}
	return &Upstream{Message: message, Status: response.StatusCode, RetryAfter: delay,
		Transient: response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500}
}

func Connection(message string, err error) error {
	summary := Summarize(err)
	return &Upstream{Message: message + summary, Transient: summary != "TLS certificate verification failed"}
}

func Retry(err error) (bool, time.Duration) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true, 0
	}
	var upstream *Upstream
	if errors.As(err, &upstream) {
		return upstream.Transient, upstream.RetryAfter
	}
	return false, 0
}
