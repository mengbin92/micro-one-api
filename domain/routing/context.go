package routing

import (
	"crypto/sha256"
	"fmt"

	"micro-one-api/pkg/jsonx"
)

const ContextVersion = 2

// SubjectFacts contains only identity-owned facts. A default is a preference,
// never a grant. Phase C accepts inherit only; fixed is a later capability.
type SubjectFacts struct {
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
	SelectionSource                string
}

func (r ResolvedRoutingContext) Validate() error {
	if r.Version != ContextVersion || r.UserID <= 0 || r.TokenID <= 0 || r.GroupID <= 0 || r.GroupKey == "" || r.TokenMode != "inherit" || r.SelectionSource != "user_default" || r.TokenRevision <= 0 || r.UserAccessRevision <= 0 || r.GroupRevision <= 0 || r.SubscriptionEntitlementVersion != 0 {
		return fmt.Errorf("invalid inherit routing context")
	}
	return nil
}

func (r ResolvedRoutingContext) Digest() string {
	b, _ := jsonx.Marshal(r)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// ResolveInherited combines fresh local grants and channel facts. Legacy
// subscriptions in phase C confer no access, so their entitlement version is 0.
func ResolveInherited(userID, tokenID int64, facts *SubjectFacts, group *Group, now int64) (*ResolvedRoutingContext, error) {
	if facts == nil || group == nil || facts.TokenMode != "inherit" || facts.TokenGroupID != 0 || facts.DefaultGroupID != group.ID || group.Status != "enabled" {
		return nil, fmt.Errorf("default routing group unavailable")
	}
	allowed := facts.PublicGroupAccess == "all" && group.AccessMode == "public"
	for _, grant := range facts.Grants {
		if grant.GroupID == group.ID && grant.Status == "active" && grant.StartsAt <= now && (grant.ExpiresAt == 0 || now < grant.ExpiresAt) {
			allowed = true
		}
	}
	if !allowed {
		return nil, fmt.Errorf("routing group access denied")
	}
	r := &ResolvedRoutingContext{Version: ContextVersion, UserID: userID, TokenID: tokenID, GroupID: group.ID, GroupKey: group.Key, TokenMode: facts.TokenMode, TokenRevision: facts.TokenRevision, UserAccessRevision: facts.AccessRevision, GroupRevision: group.Revision, SelectionSource: "user_default"}
	return r, r.Validate()
}
