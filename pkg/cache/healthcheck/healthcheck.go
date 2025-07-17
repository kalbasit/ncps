package healthcheck

import (
	"context"
	"sync"
	"time"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/rs/zerolog"
)

// HealthChecker is responsible for checking the health of upstream caches.
type HealthChecker struct {
	mu        sync.RWMutex
	upstreams []*upstream.Cache
	ticker    *time.Ticker

	// healthChangeNotifier is used to notify about health status changes
	healthChangeNotifier chan<- HealthStatusChange
}

// HealthStatusChange represents a change in upstream health status
type HealthStatusChange struct {
	Upstream  *upstream.Cache
	IsHealthy bool
}

// New creates a new HealthChecker.
func New(upstreams []*upstream.Cache) *HealthChecker {
	return &HealthChecker{
		upstreams: upstreams,
		ticker:    time.NewTicker(1 * time.Minute),
	}
}

// SetHealthChangeNotifier sets the channel to notify about health status changes
func (hc *HealthChecker) SetHealthChangeNotifier(ch chan<- HealthStatusChange) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.healthChangeNotifier = ch
}

// AddUpstream adds a new upstream cache to monitor
func (hc *HealthChecker) AddUpstream(upstream *upstream.Cache) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.upstreams = append(hc.upstreams, upstream)
}

// RemoveUpstream removes an upstream cache from monitoring
func (hc *HealthChecker) RemoveUpstream(upstream *upstream.Cache) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	for i, u := range hc.upstreams {
		if u.GetHostname() == upstream.GetHostname() {
			hc.upstreams = append(hc.upstreams[:i], hc.upstreams[i+1:]...)
			break
		}
	}
}

// Start starts the health checker.
func (hc *HealthChecker) Start(ctx context.Context) {
	go func() {
		// Run a health check at the beginning
		hc.check(ctx)

		for {
			select {
			case <-ctx.Done():
				hc.ticker.Stop()
				return
			case <-hc.ticker.C:
				hc.check(ctx)
			}
		}
	}()
}

func (hc *HealthChecker) check(ctx context.Context) {
	hc.mu.RLock()
	upstreams := make([]*upstream.Cache, len(hc.upstreams))
	copy(upstreams, hc.upstreams)
	notifier := hc.healthChangeNotifier
	hc.mu.RUnlock()

	for _, u := range upstreams {
		previouslyHealthy := u.IsHealthy()
		priority, err := u.ParsePriority(ctx)
		if err != nil {
			u.SetHealthy(false)
			zerolog.Ctx(ctx).Error().Err(err).Str("upstream", u.GetHostname()).Msg("upstream is not healthy")

			// Notify about health status change
			if previouslyHealthy && notifier != nil {
				select {
				case notifier <- HealthStatusChange{Upstream: u, IsHealthy: false}:
				default:
					// Non-blocking send
				}
			}
			continue
		}
		u.SetPriority(priority)
		u.SetHealthy(true)
		zerolog.Ctx(ctx).Debug().Str("upstream", u.GetHostname()).Uint64("priority", priority).Msg("upstream is healthy")

		// Notify about health status change
		if !previouslyHealthy && notifier != nil {
			select {
			case notifier <- HealthStatusChange{Upstream: u, IsHealthy: true}:
			default:
				// Non-blocking send
			}
		}
	}
}
