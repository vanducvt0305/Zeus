// Package httpx adds bounded retry with exponential backoff to outbound HTTP
// calls, so a transient network blip or a 429/5xx doesn't abort a crawl or an
// embedding batch. Callers pass a request builder (not a request), because a
// retried request needs a fresh body.
package httpx

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// DefaultRetries is a sensible default attempt budget for API calls.
const DefaultRetries = 3

// Do executes build()'d requests until one succeeds, the retry budget is
// exhausted, or ctx is cancelled. It retries on transport errors and on
// retryable status codes (429, 500, 502, 503, 504), honoring Retry-After.
// On the final attempt it returns whatever it got (response or error) so the
// caller can surface the real status/error.
func Do(ctx context.Context, client *http.Client, build func() (*http.Request, error), retries int) (*http.Response, error) {
	if retries < 0 {
		retries = 0
	}
	delay := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err == nil && !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt >= retries {
			return resp, err
		}

		wait := delay
		if resp != nil {
			if ra := retryAfter(resp); ra > 0 {
				wait = ra
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
