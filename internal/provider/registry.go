package provider

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// Registry aggregates providers by name and exposes the merged catalogue.
type Registry struct {
	providers []Provider
	byName    map[string]Provider
	interval  time.Duration
}

func NewRegistry(interval time.Duration, providers ...Provider) *Registry {
	byName := make(map[string]Provider, len(providers))
	for _, p := range providers {
		byName[p.Name()] = p
	}
	return &Registry{providers: providers, byName: byName, interval: interval}
}

func (r *Registry) Providers() []Provider { return r.providers }

func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// Lookup splits a public model id (e.g. "kilo/foo/bar") into its provider and
// the original id. It does not verify the model exists in the provider's
// current catalogue; that's the provider's job when forwarding.
func (r *Registry) Lookup(publicID string) (Provider, string, bool) {
	name, original, ok := strings.Cut(publicID, "/")
	if !ok || name == "" || original == "" {
		return nil, "", false
	}
	p, ok := r.byName[name]
	if !ok {
		return nil, "", false
	}
	return p, original, true
}

// Models returns the merged catalogue across all providers, in provider order.
func (r *Registry) Models() []Model {
	var out []Model
	for _, p := range r.providers {
		out = append(out, p.Models()...)
	}
	return out
}

// Statuses returns a per-provider snapshot in provider order.
func (r *Registry) Statuses() []Status {
	out := make([]Status, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Status())
	}
	return out
}

// RefreshAll calls Refresh on every provider concurrently and waits.
func (r *Registry) RefreshAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range r.providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			if err := p.Refresh(ctx); err != nil {
				log.Printf("provider %s refresh: %v", p.Name(), err)
			}
		}(p)
	}
	wg.Wait()
}

// Run blocks, refreshing every interval until ctx is cancelled.
func (r *Registry) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RefreshAll(ctx)
		}
	}
}
