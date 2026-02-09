package matrix

import (
	"context"
	"sync"

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

func waitForLimiter(limiter *ratelimit.Limiter) {
	if limiter == nil {
		return
	}
	_ = limiter.Wait(context.Background())
}
