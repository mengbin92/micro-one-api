package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-kratos/kratos/v3/registry"
)

// Resolver resolves service endpoints. It supports static endpoints and
// dynamic discovery via a registry.Discovery.
type Resolver struct {
	mu        sync.RWMutex
	static    map[string]string // serviceName -> endpoint
	discovery registry.Discovery
	next      map[string]int
}

// NewResolver creates a Resolver. If discovery is nil, only static endpoints are used.
func NewResolver(discovery registry.Discovery) *Resolver {
	return &Resolver{
		static:    make(map[string]string),
		discovery: discovery,
		next:      make(map[string]int),
	}
}

// SetStatic registers a static endpoint for a service.
func (r *Resolver) SetStatic(serviceName, endpoint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.static[serviceName] = endpoint
}

// Resolve returns an endpoint for the named service.
// It prefers discovery if available, falling back to static config.
func (r *Resolver) Resolve(ctx context.Context, serviceName string) (string, error) {
	if r.discovery != nil {
		instances, err := r.discovery.GetService(ctx, serviceName)
		if err == nil && len(instances) > 0 {
			inst := r.nextInstance(serviceName, instances)
			if len(inst.Endpoints) > 0 {
				return inst.Endpoints[0], nil
			}
		}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if ep, ok := r.static[serviceName]; ok {
		return ep, nil
	}
	return "", fmt.Errorf("service %s: no endpoint available", serviceName)
}

func (r *Resolver) nextInstance(serviceName string, instances []*registry.ServiceInstance) *registry.ServiceInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.next[serviceName] % len(instances)
	r.next[serviceName] = (idx + 1) % len(instances)
	return instances[idx]
}

// ResolveGRPC returns a gRPC-compatible endpoint (strips scheme prefix).
func (r *Resolver) ResolveGRPC(ctx context.Context, serviceName string) (string, error) {
	ep, err := r.Resolve(ctx, serviceName)
	if err != nil {
		return "", err
	}
	// Strip scheme: "grpc://host:port" -> "host:port"
	if len(ep) > 7 && ep[:7] == "grpc://" {
		return ep[7:], nil
	}
	if len(ep) > 8 && ep[:8] == "https://" {
		return ep[8:], nil
	}
	if len(ep) > 7 && ep[:7] == "http://" {
		return ep[7:], nil
	}
	return ep, nil
}
