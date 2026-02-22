package native

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	mu       sync.RWMutex
	runtimes map[StrategyID]Runtime
}

func NewRegistry() *Registry {
	return &Registry{runtimes: map[StrategyID]Runtime{}}
}

func (r *Registry) Register(rt Runtime) error {
	if r == nil {
		return fmt.Errorf("runtime registry is nil")
	}
	if rt == nil {
		return fmt.Errorf("runtime is nil")
	}
	id := rt.ID()
	if id == "" {
		return fmt.Errorf("runtime id is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runtimes[id]; exists {
		return fmt.Errorf("runtime %q already registered", id)
	}
	r.runtimes[id] = rt
	return nil
}

func (r *Registry) MustRegister(rt Runtime) {
	if err := r.Register(rt); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(id StrategyID) (Runtime, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.runtimes[id]
	return rt, ok
}

func (r *Registry) IDs() []StrategyID {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]StrategyID, 0, len(r.runtimes))
	for id := range r.runtimes {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
