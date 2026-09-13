package routing

// Source keeps channel IDs and subscription-account IDs in separate namespaces.
type Source struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

const (
	Channel      = "channel"
	Subscription = "subscription"
)

// Permission is request-model scoped. It must never be reused for another
// model or interpreted as membership in all of a source's routing groups.
type Permission struct {
	Allowed         bool
	UpstreamModelID string
}
