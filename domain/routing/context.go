package routing

import (
	"crypto/sha256"
	"fmt"

	"micro-one-api/pkg/jsonx"
)

const ContextVersion = 2

// TokenReference contains only non-secret Key metadata for impact previews.
type TokenReference struct {
	ID                int64
	Name, Mode        string
	GroupID, Revision int64
}

// SubjectFacts contains only identity-owned facts. A default is a preference,
// never a grant. Token policy and access are checked independently.
type EntitlementFacts struct {
	SubscriptionID int64
	Revision       int64
	Grants         []UserGroupGrant
}

func WithEntitlements(f *SubjectFacts, e *EntitlementFacts) *SubjectFacts {
	if f == nil || e == nil {
		return nil
	}
	copy := *f
	copy.Grants = make([]UserGroupGrant, 0, len(f.Grants)+len(e.Grants))
	for _, g := range f.Grants {
		if g.SourceType != "subscription" {
			copy.Grants = append(copy.Grants, g)
		}
	}
	copy.Grants = append(copy.Grants, e.Grants...)
	copy.SubscriptionID = e.SubscriptionID
	copy.SubscriptionEntitlementVersion = e.Revision
	return &copy
}

type SubjectFacts struct {
	SubscriptionID                 int64 `json:",omitempty"`
	SubscriptionEntitlementVersion int64
	// TokenReferences are returned only by the administrative facts query.
	TokenReferences   []TokenReference
	DefaultGroupID    int64
	PublicGroupAccess string
	AccessRevision    int64
	TokenMode         string
	TokenGroupID      int64
	TokenRevision     int64
	Grants            []UserGroupGrant
}

type UserGroupGrant struct {
	GroupID    int64
	SourceType string
	SourceRef  string
	StartsAt   int64
	ExpiresAt  int64
	Status     string
}

type ResolvedRoutingContext struct {
	Version                        int32
	UserID                         int64
	TokenID                        int64
	GroupID                        int64
	GroupKey                       string
	TokenMode                      string
	TokenRevision                  int64
	UserAccessRevision             int64
	GroupRevision                  int64
	SubscriptionEntitlementVersion int64
	SubscriptionID                 int64 `json:",omitempty"`
	SelectionSource                string
}

func (r ResolvedRoutingContext) Validate() error {
	if r.Version != ContextVersion || r.UserID <= 0 || r.TokenID <= 0 || r.GroupID <= 0 || r.GroupKey == "" || !ValidSelection(r.TokenMode, r.SelectionSource) || r.TokenRevision <= 0 || r.UserAccessRevision <= 0 || r.GroupRevision <= 0 || r.SubscriptionEntitlementVersion < 0 || r.SubscriptionID < 0 {
		return fmt.Errorf("invalid routing context")
	}
	return nil
}

func (r ResolvedRoutingContext) Digest() string {
	b, _ := jsonx.Marshal(r)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// ValidPolicy rejects missing/unknown modes and contradictory group fields.
func ValidPolicy(mode string, groupID int64) bool {
	return mode == "inherit" && groupID == 0 || mode == "fixed" && groupID > 0
}
func ValidSelection(mode, source string) bool {
	return mode == "inherit" && source == "user_default" || mode == "fixed" && source == "token_fixed"
}
func SelectedGroupID(f *SubjectFacts) int64 {
	if f == nil || !ValidPolicy(f.TokenMode, f.TokenGroupID) {
		return 0
	}
	if f.TokenMode == "fixed" {
		return f.TokenGroupID
	}
	return f.DefaultGroupID
}

// AccessSources evaluates current facts, including time boundaries, without
// treating a default preference or a legacy subscription as access.
func AccessSources(f *SubjectFacts, g *Group, now int64) []UserGroupGrant {
	if f == nil || g == nil || g.Status != "enabled" {
		return nil
	}
	var sources []UserGroupGrant
	if f.PublicGroupAccess == "all" && g.AccessMode == "public" {
		sources = append(sources, UserGroupGrant{GroupID: g.ID, SourceType: "public", Status: "active"})
	}
	for _, grant := range f.Grants {
		if grant.GroupID == g.ID && grant.Status == "active" && grant.StartsAt <= now && (grant.ExpiresAt == 0 || now < grant.ExpiresAt) {
			sources = append(sources, grant)
		}
	}
	return sources
}
func Resolve(userID, tokenID int64, facts *SubjectFacts, group *Group, now int64) (*ResolvedRoutingContext, error) {
	if group == nil || SelectedGroupID(facts) <= 0 || SelectedGroupID(facts) != group.ID || group.Status != "enabled" {
		return nil, fmt.Errorf("routing group unavailable")
	}
	if len(AccessSources(facts, group, now)) == 0 {
		return nil, fmt.Errorf("routing group access denied")
	}
	source := "user_default"
	if facts.TokenMode == "fixed" {
		source = "token_fixed"
	}
	r := &ResolvedRoutingContext{Version: ContextVersion, UserID: userID, TokenID: tokenID, GroupID: group.ID, GroupKey: group.Key, TokenMode: facts.TokenMode, TokenRevision: facts.TokenRevision, UserAccessRevision: facts.AccessRevision, GroupRevision: group.Revision, SubscriptionID: facts.SubscriptionID, SubscriptionEntitlementVersion: facts.SubscriptionEntitlementVersion, SelectionSource: source}
	return r, r.Validate()
}

// ResolveInherited retains the explicit phase C contract for older callers.
func ResolveInherited(userID, tokenID int64, facts *SubjectFacts, group *Group, now int64) (*ResolvedRoutingContext, error) {
	if facts == nil || facts.TokenMode != "inherit" {
		return nil, fmt.Errorf("default routing group unavailable")
	}
	return Resolve(userID, tokenID, facts, group, now)
}

func SessionKey(c *ResolvedRoutingContext, key string) string {
	if c == nil || key == "" {
		return key
	}
	return fmt.Sprintf("v2/u%d/t%d/g%d/%s", c.UserID, c.TokenID, c.GroupID, key)
}
