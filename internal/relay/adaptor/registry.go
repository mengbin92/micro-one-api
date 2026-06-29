package adaptor

import (
	"fmt"
	"sync"
)

// channelType constants intentionally mirror provider.ChannelType* so that the
// registry keys stay stable across packages without a circular import. They
// are unexported; callers pass the provider constants by value.

var (
	registryMu sync.RWMutex
	registry   = map[int32]func() Adaptor{}
)

// Register registers a factory for a channel type. It is safe to call from
// init() across packages. Registering the same type twice overwrites the
// previous entry (last-write-wins), matching new-api's GetAdaptor behavior.
func Register(channelType int32, factory func() Adaptor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[channelType] = factory
}

// GetAdaptor returns a new instance of the adaptor for the given channel
// type. Returns false if no adaptor is registered for that type.
func GetAdaptor(channelType int32) (Adaptor, bool) {
	registryMu.RLock()
	factory, ok := registry[channelType]
	registryMu.RUnlock()
	if !ok {
		return nil, false
	}
	return factory(), true
}

// MustGetAdaptor returns the adaptor for the channel type or panics. Useful
// only for tests with a known registration.
func MustGetAdaptor(channelType int32) Adaptor {
	a, ok := GetAdaptor(channelType)
	if !ok {
		panic(fmt.Sprintf("adaptor: no adaptor registered for channel type %d", channelType))
	}
	return a
}

// RegisteredTypes returns the channel types that currently have a registered
// adaptor. Primarily for diagnostics and tests.
func RegisteredTypes() []int32 {
	registryMu.RLock()
	defer registryMu.RUnlock()
	types := make([]int32, 0, len(registry))
	for t := range registry {
		types = append(types, t)
	}
	return types
}
