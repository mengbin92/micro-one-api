// Package billing owns contracts shared by relay execution and billing.
package billing

// Upstream source kinds identify the credential/account namespace used for an
// upstream call. They are shared so relay usage events and billing ledgers
// cannot drift through duplicated string literals.
const (
	SourceKindChannel      = "channel"
	SourceKindSubscription = "subscription"
)
