package matrix

import (
	"context"
	"sync"
	"time"

	"github.com/shawkym/agentpipe/pkg/log"
	"github.com/shawkym/agentpipe/pkg/ratelimit"
)

var limiterRegistry sync.Map

const (
	limiterSafetyRatio = 0.10
	limiterSafetyMin   = 25 * time.Millisecond
	limiterSafetyMax   = 500 * time.Millisecond
)

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
		margin := limiterSafetyMargin(wait)
		log.WithFields(map[string]interface{}{
			"call":    call,
			"reason":  "rate_limit",
			"wait_ms": (wait + margin).Milliseconds(),
			"margin_ms": func() int64 {
				if margin <= 0 {
					return 0
				}
				return margin.Milliseconds()
			}(),
		}).Info("matrix api wait")
		if margin > 0 {
			time.Sleep(margin)
		}
	}

	_ = limiter.Wait(context.Background())
}

func limiterSafetyMargin(wait time.Duration) time.Duration {
	if wait <= 0 {
		return 0
	}
	margin := time.Duration(float64(wait) * limiterSafetyRatio)
	if margin < limiterSafetyMin {
		margin = limiterSafetyMin
	}
	if margin > limiterSafetyMax {
		margin = limiterSafetyMax
	}
	return margin
}
