package matrix

import (
	"context"
	"sync"
	"time"

	"github.com/shawkym/agentpipe/pkg/log"
	"github.com/shawkym/agentpipe/pkg/ratelimit"
)

var limiterRegistry sync.Map

func limiterFor(baseURL string, rate float64, burst int) *ratelimit.Limiter {
	key := cleanBaseURL(baseURL)
	if existing, ok := limiterRegistry.Load(key); ok {
		limiter := existing.(*ratelimit.Limiter)
		limiter.SetRate(rate)
		limiter.SetBurst(burst)
		return limiter
	}

	limiter := ratelimit.NewLimiter(rate, burst)
	actual, _ := limiterRegistry.LoadOrStore(key, limiter)
	return actual.(*ratelimit.Limiter)
}

func waitForLimiter(limiter *ratelimit.Limiter, call string) {
	if limiter == nil {
		return
	}

	stats := limiter.GetStats()
	if !stats.Disabled && stats.Rate > 0 && stats.AvailableTokens < 1 {
		wait := time.Duration((1.0 - stats.AvailableTokens) / stats.Rate * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		log.WithFields(map[string]interface{}{
			"call":    call,
			"reason":  "rate_limit",
			"wait_ms": wait.Milliseconds(),
		}).Info("matrix api wait")
	}

	_ = limiter.Wait(context.Background())
}
