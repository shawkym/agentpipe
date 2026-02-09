package matrix

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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

func parseRetryAfter(resp *http.Response, body []byte) time.Duration {
	headerDelay := parseRetryAfterHeader(resp)
	bodyDelay := parseRetryAfterBody(body)
	if bodyDelay > headerDelay {
		return bodyDelay
	}
	return headerDelay
}

func parseRetryAfterBody(body []byte) time.Duration {
	var payload rateLimitPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	if payload.RetryAfterMs <= 0 {
		return 0
	}
	return time.Duration(payload.RetryAfterMs) * time.Millisecond
}

func parseRetryAfterHeader(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	if parsed, err := http.ParseTime(raw); err == nil {
		wait := time.Until(parsed)
		if wait > 0 {
			return wait
		}
	}

	return 0
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

func sleepWithPacer(pacer *Pacer, call, reason string, d time.Duration) {
	if d <= 0 {
		return
	}
	if reason == "retry_after" && pacer != nil {
		pacer.Pause(d)
	}
	log.WithFields(map[string]interface{}{
		"call":    call,
		"reason":  reason,
		"wait_ms": d.Milliseconds(),
	}).Info("matrix api wait")
	time.Sleep(d)
}

func sleepWithLog(call, reason string, d time.Duration) {
	sleepWithPacer(nil, call, reason, d)
}
