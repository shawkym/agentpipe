package matrix

import (
	"encoding/json"
	"time"

	"github.com/shawkym/agentpipe/pkg/log"
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

func sleepWithLog(call, reason string, d time.Duration) {
	if d <= 0 {
		return
	}
	log.WithFields(map[string]interface{}{
		"call":    call,
		"reason":  reason,
		"wait_ms": d.Milliseconds(),
	}).Info("matrix api wait")
	time.Sleep(d)
}
