package adaptor

import "errors"

// Sentinel errors returned by the MVP adaptors and the lazyAdaptor wrapper.
var (
	// errNotInitialized is returned when a lazyAdaptor method is called before
	// Init. The server layer always calls Init first, so this only surfaces in
	// misuse or tests.
	errNotInitialized = errors.New("adaptor: Init was not called")

	// errNoFactory is returned when the registry's provider factory has not
	// been wired via SetProviderFactory.
	errNoFactory = errors.New("adaptor: provider factory is not configured (call SetProviderFactory)")

	// errNoChannel is returned when a relay context has no channel (e.g. a
	// subscription-account request that did not populate Account either).
	errNoChannel = errors.New("adaptor: relay context has no channel")

	// errNoTokenProviderFactory is returned when the OAuth adaptor registry has
	// not been wired via SetTokenProviderFactory.
	errNoTokenProviderFactory = errors.New("adaptor: token provider factory is not configured (call SetTokenProviderFactory)")

	// errUnknownOAuthPlatform is returned when an OAuth adaptor is requested for
	// a platform that has no registered adaptor.
	errUnknownOAuthPlatform = errors.New("adaptor: unknown OAuth platform")
)
