package registry

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v3/registry"
)

func TestResolver_StaticFallback(t *testing.T) {
	r := NewResolver(nil)
	r.SetStatic("identity-service", "127.0.0.1:9001")

	ep, err := r.Resolve(context.Background(), "identity-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep != "127.0.0.1:9001" {
		t.Errorf("expected 127.0.0.1:9001, got %s", ep)
	}
}

func TestResolver_MissingService(t *testing.T) {
	r := NewResolver(nil)

	_, err := r.Resolve(context.Background(), "unknown-service")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestResolverGRPC_StripScheme(t *testing.T) {
	r := NewResolver(nil)
	r.SetStatic("svc", "grpc://10.0.0.1:9000")

	ep, err := r.ResolveGRPC(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep != "10.0.0.1:9000" {
		t.Errorf("expected 10.0.0.1:9000, got %s", ep)
	}
}

func TestResolverGRPC_NoScheme(t *testing.T) {
	r := NewResolver(nil)
	r.SetStatic("svc", "10.0.0.1:9000")

	ep, err := r.ResolveGRPC(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep != "10.0.0.1:9000" {
		t.Errorf("expected 10.0.0.1:9000, got %s", ep)
	}
}

// mockDiscovery implements registry.Discovery for testing.
type mockDiscovery struct {
	services map[string][]*registry.ServiceInstance
}

func (m *mockDiscovery) GetService(ctx context.Context, name string) ([]*registry.ServiceInstance, error) {
	instances, ok := m.services[name]
	if !ok || len(instances) == 0 {
		return nil, nil
	}
	return instances, nil
}

func (m *mockDiscovery) Watch(ctx context.Context, name string) (registry.Watcher, error) {
	return nil, nil
}

func TestResolver_DiscoveryPreferred(t *testing.T) {
	discovery := &mockDiscovery{
		services: map[string][]*registry.ServiceInstance{
			"identity-service": {
				{ID: "1", Name: "identity-service", Endpoints: []string{"grpc://10.0.0.5:9001"}},
			},
		},
	}

	r := NewResolver(discovery)
	r.SetStatic("identity-service", "127.0.0.1:9001")

	ep, err := r.ResolveGRPC(context.Background(), "identity-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep != "10.0.0.5:9001" {
		t.Errorf("expected discovery endpoint 10.0.0.5:9001, got %s", ep)
	}
}

func TestResolver_DiscoveryRoundRobin(t *testing.T) {
	discovery := &mockDiscovery{
		services: map[string][]*registry.ServiceInstance{
			"identity-service": {
				{ID: "1", Name: "identity-service", Endpoints: []string{"grpc://10.0.0.5:9001"}},
				{ID: "2", Name: "identity-service", Endpoints: []string{"grpc://10.0.0.6:9001"}},
			},
		},
	}

	r := NewResolver(discovery)
	for _, want := range []string{"grpc://10.0.0.5:9001", "grpc://10.0.0.6:9001", "grpc://10.0.0.5:9001"} {
		got, err := r.Resolve(context.Background(), "identity-service")
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		if got != want {
			t.Fatalf("Resolve = %q, want %q", got, want)
		}
	}
}

func TestResolver_DiscoveryFallbackToStatic(t *testing.T) {
	discovery := &mockDiscovery{
		services: map[string][]*registry.ServiceInstance{},
	}

	r := NewResolver(discovery)
	r.SetStatic("identity-service", "127.0.0.1:9001")

	ep, err := r.ResolveGRPC(context.Background(), "identity-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep != "127.0.0.1:9001" {
		t.Errorf("expected static endpoint 127.0.0.1:9001, got %s", ep)
	}
}

func TestResolveHost(t *testing.T) {
	host := ResolveHost()
	if host == "" {
		t.Fatal("expected non-empty host")
	}
}

func TestParseDuration(t *testing.T) {
	d := ParseDuration("5s", 10)
	if d.Seconds() != 5 {
		t.Errorf("expected 5s, got %v", d)
	}

	d = ParseDuration("", 10)
	if d != 10 {
		t.Errorf("expected default 10, got %v", d)
	}

	d = ParseDuration("invalid", 10)
	if d != 10 {
		t.Errorf("expected default 10 for invalid input, got %v", d)
	}
}
