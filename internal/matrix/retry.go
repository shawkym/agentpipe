package matrix

import (
	"encoding/json"
	"time"
)

const (
	defaultRateLimitRetries = 6
	maxRetryAfter           = 2 * time.Minute
)

type rateLimitPayload struct {
	ErrCode      string `json:"errcode"`
	RetryAfterMs int    `json:"retry_after_ms"`
}

func parseRetryAfter(body []byte) time.Duration {
	var payload rateLimitPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	if payload.ErrCode != "M_LIMIT_EXCEEDED" || payload.RetryAfterMs <= 0 {
		return 0
	}
	return time.Duration(payload.RetryAfterMs) * time.Millisecond
}

func capRetryAfter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}
