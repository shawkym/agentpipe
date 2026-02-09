package matrix

import (
	"context"
	"sync"
	"time"

	"github.com/shawkym/agentpipe/pkg/log"
)

var pacerRegistry sync.Map

const (
	pacerSafetyRatio = 0.10
	pacerSafetyMin   = 25 * time.Millisecond
	pacerSafetyMax   = 500 * time.Millisecond
)

// Pacer spaces Matrix API calls and honors Retry-After cooldowns.
// It is shared per homeserver to avoid bursts during auto-provisioning and sync.
type Pacer struct {
	mu            sync.Mutex
	minInterval   time.Duration
	next          time.Time
	cooldownUntil time.Time
	disabled      bool
}

func pacerFor(baseURL string, rate float64) *Pacer {
	key := cleanBaseURL(baseURL)
	if existing, ok := pacerRegistry.Load(key); ok {
		pacer := existing.(*Pacer)
		pacer.SetRate(rate)
		return pacer
	}

	pacer := newPacer(rate)
	actual, _ := pacerRegistry.LoadOrStore(key, pacer)
	return actual.(*Pacer)
}

func newPacer(rate float64) *Pacer {
	p := &Pacer{}
	p.SetRate(rate)
	return p
}

// SetRate updates the pacing rate in requests per second.
// A rate <= 0 disables pacing but still respects Retry-After cooldowns.
func (p *Pacer) SetRate(rate float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if rate <= 0 {
		p.disabled = true
		p.minInterval = 0
		return
	}

	p.disabled = false
	interval := time.Duration(float64(time.Second) / rate)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	p.minInterval = interval
}

// Pause sets a cooldown window (Retry-After) that all calls must respect.
func (p *Pacer) Pause(d time.Duration) {
	if d <= 0 || p == nil {
		return
	}
	until := time.Now().Add(d)
	p.mu.Lock()
	if until.After(p.cooldownUntil) {
		p.cooldownUntil = until
	}
	p.mu.Unlock()
}

func waitForPacer(pacer *Pacer, call string) {
	if pacer == nil {
		return
	}
	_ = pacer.Wait(context.Background(), call)
}

// Wait blocks until the pacer allows the request or the context is canceled.
func (p *Pacer) Wait(ctx context.Context, call string) error {
	if p == nil {
		return nil
	}

	now := time.Now()
	scheduled, reason := p.reserve(now)
	wait := scheduled.Sub(now)
	if wait <= 0 {
		return nil
	}

	margin := pacerSafetyMargin(wait)
	total := wait + margin
	log.WithFields(map[string]interface{}{
		"call":    call,
		"reason":  reason,
		"wait_ms": total.Milliseconds(),
		"margin_ms": func() int64 {
			if margin <= 0 {
				return 0
			}
			return margin.Milliseconds()
		}(),
	}).Info("matrix api wait")

	timer := time.NewTimer(total)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pacer) reserve(now time.Time) (time.Time, string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	scheduled := now
	reason := ""

	if p.cooldownUntil.After(scheduled) {
		scheduled = p.cooldownUntil
		reason = "retry_after"
	}

	if !p.disabled && p.minInterval > 0 {
		if p.next.After(scheduled) {
			scheduled = p.next
			reason = "rate_limit"
		}
		p.next = scheduled.Add(p.minInterval)
	}

	return scheduled, reason
}

func pacerSafetyMargin(wait time.Duration) time.Duration {
	if wait <= 0 {
		return 0
	}
	margin := time.Duration(float64(wait) * pacerSafetyRatio)
	if margin < pacerSafetyMin {
		margin = pacerSafetyMin
	}
	if margin > pacerSafetyMax {
		margin = pacerSafetyMax
	}
	return margin
}
