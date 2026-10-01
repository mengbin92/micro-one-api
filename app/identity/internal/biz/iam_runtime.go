package biz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/audit"
	sessionauth "micro-one-api/platform/security/auth"
)

// IAMRuntimeRepo joins account and session changes to the policy-locked transaction.
// Explicit field names are the account write contract, not storage column names.
type IAMRuntimeRepo interface {
	IAMConstraintRepo
	FindOAuthIdentityTx(context.Context, IAMTx, string, string) (*OAuthIdentity, error)
	FindOAuthIdentityByUserTx(context.Context, IAMTx, int64, string) (*OAuthIdentity, error)
	UpdateAccount(context.Context, IAMTx, User, []string) error
	UpdateRoutingAccessTx(context.Context, IAMTx, RoutingAccessChange) error
	DeleteAccount(context.Context, IAMTx, int64) error
	Session(context.Context, IAMTx, string, authorization.Context) (IAMSessionContext, error)
	CreateSession(context.Context, IAMTx, authorization.Actor, IAMSessionContext) error
	RevokeSession(context.Context, IAMTx, string, authorization.Context, bool, time.Time, uint64) error
	RevokeUserSessions(context.Context, IAMTx, int64, time.Time) error
}

func (uc *IdentityUsecase) SetIAMRuntime(repo IAMRuntimeRepo, runner IAMTxRunner) {
	uc.iam, uc.iamRunner = repo, runner
}

func (uc *IdentityUsecase) authenticateSession(raw string) (authorization.Actor, error) {
	a, err := sessionauth.VerifyUserSession(raw, uc.sessionSecret, uc.sessionIssuer, uc.now())
	if err != nil {
		return a, ErrInvalidToken
	}
	return a, nil
}
func checkIAMIdentity(a authorization.Actor, u User, now time.Time) error {
	if a.UserID <= 0 || a.UserID != u.ID || strings.TrimSpace(a.SessionID) == "" || !now.Before(a.ExpiresAt) {
		return ErrInvalidToken
	}
	if u.Status != UserStatusEnabled {
		return ErrUserDisabled
	}
	if a.PasswordEpoch < u.PasswordChangedAt {
		return ErrSessionRevoked
	}
	return nil
}
func checkIAMSession(a authorization.Actor, s IAMSessionContext, now time.Time) error {
	if s.UserID != a.UserID || s.SessionID != a.SessionID || !s.ExpiresAt.Equal(a.ExpiresAt) || s.RevokedAt != nil || s.ContextRevokedAt != nil || !now.Before(s.ExpiresAt) {
		return ErrSessionRevoked
	}
	return nil
}

var errIAMNoChange = errors.New("IAM no change")

func (uc *IdentityUsecase) runtimeWrite(ctx context.Context, action, target, reason string, actor authorization.Actor, fn func(context.Context, IAMTx) error) error {
	return uc.runtimeWriteEvent(ctx, action, target, reason, actor, func(ctx context.Context, tx IAMTx, _ *IAMAuditEvent) error { return fn(ctx, tx) })
}
func (uc *IdentityUsecase) runtimeWriteEvent(ctx context.Context, action, target, reason string, actor authorization.Actor, fn func(context.Context, IAMTx, *IAMAuditEvent) error) error {
	if uc.iam == nil || uc.iamRunner == nil {
		return ErrIAMDependencyUnavailable
	}
	e := IAMAuditEvent{EventID: uc.generateToken(), Actor: actor, Context: authorization.Platform(), TargetContext: authorization.Platform(), Action: action, Target: target, Reason: reason, Before: "{}", After: "{}", Diff: "{}", Result: "success"}
	changed := false
	err := uc.iamRunner.RunIAMWrite(ctx, func(ctx context.Context, tx IAMTx) error {
		changed = false
		e.OccurredAt = uc.now().UTC()
		e.Target = target
		e.Before, e.After, e.Diff = "{}", "{}", "{}"
		e.Versions = authorization.Versions{}
		if err := fn(ctx, tx, &e); err != nil {
			if errors.Is(err, errIAMNoChange) {
				return nil
			}
			return err
		}
		changed = true
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		e.Versions.Policy = p.PolicyRevision
		e.Versions.Catalog = p.CatalogRevision
		userID := actor.UserID
		if strings.HasPrefix(action, "account.") || strings.HasPrefix(action, "credential.") {
			if id, parseErr := strconv.ParseInt(e.Target, 10, 64); parseErr == nil {
				userID = id
			}
		}
		if userID > 0 {
			rev, err := uc.iam.UserRevision(ctx, tx, userID)
			if err != nil && !errors.Is(err, ErrIAMNotFound) {
				return err
			}
			e.Versions.User = rev
		}
		if actor.SessionID != "" {
			session, err := uc.iam.Session(ctx, tx, actor.SessionID, authorization.Platform())
			if err != nil && !errors.Is(err, ErrIAMNotFound) {
				return err
			}
			e.Versions.Session = session.SessionRevision
			e.Versions.SessionContext = session.Revision
		}
		return uc.iam.AppendAudit(ctx, tx, e)
	})
	if err != nil {
		e.Result = "failure"
		e.OccurredAt = uc.now().UTC()
		if auditErr := uc.iam.AppendFailureAudit(ctx, e); auditErr != nil {
			return errors.Join(err, fmt.Errorf("failure audit: %w", auditErr))
		}
		return err
	}
	if !changed {
		return nil
	}
	// A retried callback contains no external side effects; log only after commit.
	uc.auditor.Log(ctx, audit.AuditEvent{EventType: audit.EventTypePermission, Actor: audit.ActorInfo{UserID: actor.UserID, ServiceName: actor.ServiceID, SessionID: actor.SessionID}, Action: action, Result: "success", Resource: audit.ResourceInfo{Type: "authorization", ID: e.Target}, Details: map[string]any{"event_id": e.EventID, "reason": reason, "versions": e.Versions}})
	return nil
}

// createIAMAccount is the sole persistent creation path, including bootstrap
// and OAuth binding. Channel facts have already been checked outside retries.
func (uc *IdentityUsecase) createIAMAccount(ctx context.Context, input User, bootstrap, allowIAM bool) (User, bool, error) {
	var saved User
	created := false
	action, code, origin := "account.create", "member", "default"
	if bootstrap {
		action, code, origin = "account.bootstrap", "root", "bootstrap"
	}
	err := uc.runtimeWriteEvent(ctx, action, input.Username, "account initialization", authorization.Actor{ServiceID: "identity-account"}, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		saved = User{}
		created = false
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		kind := authorization.LegacyAccountWrite
		if p.Mode == "iam" && allowIAM {
			kind = authorization.IAMManagementWrite
		}
		if bootstrap {
			kind = authorization.BootstrapWrite
		}
		if p.CheckWrite(kind, false) != nil {
			return ErrIAMCutoverBlocked
		}
		if bootstrap {
			n, err := uc.iam.CountUsers(ctx, tx)
			if err != nil {
				return err
			}
			if n > 0 {
				return errIAMNoChange
			}
		}
		if input.Role != RoleCommonUser && !(bootstrap && input.Role == RoleRootUser) {
			return ErrIAMProtected
		}
		if input.InviterID > 0 {
			if _, err = uc.iam.User(ctx, tx, input.InviterID); err != nil {
				return err
			}
		}
		saved, err = uc.iam.CreateUser(ctx, tx, input)
		if err != nil {
			return err
		}
		state, err := uc.iam.ConstraintState(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		roleID := int64(0)
		for _, r := range state.Roles {
			if r.Code == code && r.Builtin && r.Status == "enabled" {
				roleID = r.ID
			}
		}
		if roleID == 0 {
			return ErrIAMInvalidRelation
		}
		a := IAMAssignment{UserID: saved.ID, RoleID: roleID, Context: authorization.Platform(), Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Validity: authorization.Interval{StartsAt: uc.now().UTC()}, Origin: origin}
		// A temporary unique ID permits pure whole-state preflight before insert.
		a.ID = 1
		for _, old := range state.Assignments {
			if old.ID >= a.ID {
				a.ID = old.ID + 1
			}
		}
		state.Assignments = append(state.Assignments, a)
		conflicts, err := IAMCheckConstraints(uc.now(), state)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return ErrIAMConstraintsViolated
		}
		a.ID = 0
		if _, err = uc.iam.SaveAssignment(ctx, tx, a, 0); err != nil {
			return err
		}
		if input.OAuthProvider != "" || input.OAuthID != "" {
			if input.OAuthProvider == "" || input.OAuthID == "" {
				return ErrOAuthUserNotFound
			}
			_, err = uc.iam.CreateOAuthIdentity(ctx, tx, OAuthIdentity{UserID: saved.ID, Provider: input.OAuthProvider, ProviderID: input.OAuthID, CreatedAt: uc.now().Unix(), UpdatedAt: uc.now().Unix()})
			if err != nil {
				return err
			}
		}
		event.Target = strconv.FormatInt(saved.ID, 10)
		after, err := jsonx.Marshal(map[string]any{"user_id": saved.ID, "role_id": roleID, "origin": origin, "context_key": "platform"})
		if err != nil {
			return err
		}
		event.After = string(after)
		event.Diff = string(after)
		created = true
		return nil
	})
	if err != nil {
		return User{}, false, err
	}
	return saved, created, nil
}

// mutateLegacyAccount re-reads the target under policy lock. This compatibility
// path permanently refuses IAM; B1 supplies authorized IAM account mutations.
func (uc *IdentityUsecase) mutateLegacyAccount(ctx context.Context, id int64, action string, fields []string, change func(*User) error) error {
	return uc.mutateLegacyAccountChecked(ctx, id, action, fields, nil, change)
}
func (uc *IdentityUsecase) mutateLegacyAccountChecked(ctx context.Context, id int64, action string, fields []string, verify func(context.Context, IAMTx) error, change func(*User) error) error {
	if uc.iam == nil {
		u, err := uc.repo.FindUserByID(ctx, id)
		if err != nil {
			return err
		}
		if err = change(u); err != nil {
			return err
		}
		return uc.repo.UpdateUser(ctx, u)
	}
	return uc.runtimeWriteEvent(ctx, action, strconv.FormatInt(id, 10), "legacy account mutation", authorization.Actor{ServiceID: "identity-legacy-account"}, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.LegacyAccountWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		if verify != nil {
			if err = verify(ctx, tx); err != nil {
				return err
			}
		}
		u, err := uc.iam.User(ctx, tx, id)
		if err != nil {
			return err
		}
		old := u
		before, _ := jsonx.Marshal(iamAccountAuditState(u))
		event.Before = string(before)
		if err = change(&u); err != nil {
			return err
		}
		if err = uc.iam.UpdateAccount(ctx, tx, u, fields); err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = uc.iam.AdvanceUser(ctx, tx, id, rev); err != nil {
			return err
		}
		if old.PasswordChangedAt != u.PasswordChangedAt || old.Status != u.Status {
			if err = uc.iam.RevokeUserSessions(ctx, tx, id, uc.now()); err != nil {
				return err
			}
		}
		after, _ := jsonx.Marshal(iamAccountAuditState(u))
		event.After = string(after)
		diff, _ := jsonx.Marshal(map[string]any{"fields": fields})
		event.Diff = string(diff)
		return uc.iam.AdvancePolicy(ctx, tx, p.PolicyRevision, false)
	})
}

// IAMAuthorizationSnapshot preserves independent grant paths and forced denies.
// It is a request-local view; callers must obtain a new one for a new decision.
type IAMAuthorizationSnapshot struct {
	Actor                            authorization.Actor
	User                             User
	Policy                           authorization.PolicyState
	Context                          authorization.Context
	Session                          IAMSessionContext
	AuthorizedRoleIDs, ActiveRoleIDs []int64
	Sources                          []authorization.GrantSource
	Versions                         authorization.Versions
	ValidUntil                       time.Time
}

func iamAuthorizedRoles(now time.Time, state IAMConstraintState, uid int64) ([]int64, error) {
	closures, err := IAMRoleClosures(state.Context, state.Roles)
	if err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	for _, a := range state.Assignments {
		if a.UserID == uid && !a.Revoked && a.Validity.Contains(now) {
			for _, id := range closures[a.RoleID] {
				ids[id] = true
			}
		}
	}
	return iamSortedIDs(ids), nil
}
func iamCurrentActive(now time.Time, state IAMConstraintState, s IAMSessionContext) ([]int64, error) {
	authorized, err := iamAuthorizedRoles(now, state, s.UserID)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, id := range s.ActiveRoleIDs {
		if slices.Contains(authorized, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func iamSessionConflicts(now time.Time, state IAMConstraintState, s IAMSessionContext) error {
	// Only evaluate this session's constraints; other users' conflicts are not
	// disclosed by a self operation. Membership/SSD still applies to its owner.
	state.Sessions = []IAMSessionContext{s}
	conflicts, err := IAMCheckConstraints(now, state)
	if err != nil {
		return err
	}
	for _, c := range conflicts {
		if slices.Contains(c.UserIDs, s.UserID) || slices.Contains(c.SessionIDs, s.SessionID) {
			return ErrIAMConstraintsViolated
		}
	}
	return nil
}

func (uc *IdentityUsecase) ensureIAMSession(ctx context.Context, a authorization.Actor) error {
	return uc.runtimeWrite(ctx, "session.initialize", a.SessionID, "verified JWT platform session", a, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.Mode != "iam" || p.Cutover != "complete" {
			return ErrIAMCutoverBlocked
		}
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = checkIAMIdentity(a, u, uc.now()); err != nil {
			return err
		}
		s, err := uc.iam.Session(ctx, tx, a.SessionID, authorization.Platform())
		if err == nil {
			if err := checkIAMSession(a, s, uc.now()); err != nil {
				return err
			}
			return errIAMNoChange
		}
		if !errors.Is(err, ErrIAMNotFound) {
			return err
		}
		state, err := uc.iam.ConstraintState(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		roles, err := iamAuthorizedRoles(uc.now(), state, a.UserID)
		if err != nil {
			return err
		}
		s = IAMSessionContext{SessionID: a.SessionID, UserID: a.UserID, Context: authorization.Platform(), ExpiresAt: a.ExpiresAt, ActivationState: "active", ActiveRoleIDs: roles, SessionRevision: 1, Revision: 1}
		if err = iamSessionConflicts(uc.now(), state, s); err != nil {
			if !errors.Is(err, ErrIAMConstraintsViolated) {
				return err
			}
			s.ActivationState = "selection_required"
			s.ActiveRoleIDs = nil
		}
		return uc.iam.CreateSession(ctx, tx, a, s)
	})
}

func (uc *IdentityUsecase) readIAMAuthorization(ctx context.Context, a authorization.Actor, c authorization.Context) (*IAMAuthorizationSnapshot, error) {
	if c.RequirePlatform() != nil {
		return nil, ErrIAMContextInvalid
	}
	var out *IAMAuthorizationSnapshot
	err := uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		now := uc.now()
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.Mode != "iam" || p.Cutover != "complete" {
			return ErrIAMCutoverBlocked
		}
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = checkIAMIdentity(a, u, now); err != nil {
			return err
		}
		s, err := uc.iam.Session(ctx, tx, a.SessionID, c)
		if err != nil {
			return err
		}
		if err = checkIAMSession(a, s, now); err != nil {
			return err
		}
		state, err := uc.iam.ConstraintState(ctx, tx, c)
		if err != nil {
			return err
		}
		authorized, err := iamAuthorizedRoles(now, state, u.ID)
		if err != nil {
			return err
		}
		active, err := iamCurrentActive(now, state, s)
		if err != nil {
			return err
		}
		if s.ActivationState != "active" {
			active = nil
		}
		s.ActiveRoleIDs = active
		if err = iamSessionConflicts(now, state, s); err != nil {
			return err
		}
		assignments := []IAMAssignment{}
		validUntil := a.ExpiresAt
		roles := map[int64]IAMRole{}
		for _, r := range state.Roles {
			roles[r.ID] = r
		}
		for _, assignment := range state.Assignments {
			if assignment.UserID != u.ID {
				continue
			}
			assignments = append(assignments, assignment)
			if !assignment.Revoked {
				for _, t := range []*time.Time{&assignment.Validity.StartsAt, assignment.Validity.ExpiresAt} {
					if t != nil && t.After(now) && t.Before(validUntil) {
						validUntil = *t
					}
				}
			}
		}
		sources, err := IAMSources(c, roles, assignments, active)
		if err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		// Never return password hashes in a cross-service authorization view.
		u.PasswordHash = ""
		out = &IAMAuthorizationSnapshot{Actor: a, User: u, Policy: p, Context: c, Session: s, AuthorizedRoleIDs: authorized, ActiveRoleIDs: active, Sources: sources, ValidUntil: validUntil, Versions: authorization.Versions{User: rev, Policy: p.PolicyRevision, Catalog: p.CatalogRevision, Session: s.SessionRevision, SessionContext: s.Revision}}
		return nil
	})
	return out, err
}

func (uc *IdentityUsecase) GetSessionAuthorization(ctx context.Context, raw string, c authorization.Context) (*IAMAuthorizationSnapshot, error) {
	if c.RequirePlatform() != nil {
		return nil, ErrIAMContextInvalid
	}
	if uc.iam == nil || uc.iamRunner == nil {
		return nil, ErrIAMDependencyUnavailable
	}
	a, err := uc.authenticateSession(raw)
	if err != nil {
		return nil, err
	}
	out, err := uc.readIAMAuthorization(ctx, a, c)
	if !errors.Is(err, ErrIAMNotFound) {
		return out, err
	}
	if err = uc.ensureIAMSession(ctx, a); err != nil {
		return nil, err
	}
	return uc.readIAMAuthorization(ctx, a, c)
}

func (uc *IdentityUsecase) ActivateSessionRoles(ctx context.Context, raw string, c authorization.Context, ids []int64, expected uint64, reason string) (*IAMAuthorizationSnapshot, error) {
	if c.RequirePlatform() != nil {
		return nil, ErrIAMContextInvalid
	}
	if !iamUniquePositive(ids) || expected == 0 || strings.TrimSpace(reason) == "" {
		return nil, ErrIAMInvalidRelation
	}
	a, err := uc.authenticateSession(raw)
	if err != nil {
		return nil, err
	}
	err = uc.runtimeWrite(ctx, "session.activate", a.SessionID, reason, a, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.IAMManagementWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = checkIAMIdentity(a, u, uc.now()); err != nil {
			return err
		}
		s, err := uc.iam.Session(ctx, tx, a.SessionID, c)
		if err != nil {
			return err
		}
		if err = checkIAMSession(a, s, uc.now()); err != nil {
			return err
		}
		if s.Revision != expected {
			return ErrIAMRevisionConflict
		}
		state, err := uc.iam.ConstraintState(ctx, tx, c)
		if err != nil {
			return err
		}
		authorized, err := iamAuthorizedRoles(uc.now(), state, a.UserID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !slices.Contains(authorized, id) {
				return ErrIAMProtected
			}
		}
		s.ActiveRoleIDs = slices.Clone(ids)
		s.ActivationState = "active"
		if err = iamSessionConflicts(uc.now(), state, s); err != nil {
			return err
		}
		return uc.iam.SaveActivation(ctx, tx, c, IAMActivationChange{SessionID: a.SessionID, RoleIDs: ids}, expected)
	})
	if err != nil {
		return nil, err
	}
	return uc.readIAMAuthorization(ctx, a, c)
}

func (uc *IdentityUsecase) RevokeOwnSession(ctx context.Context, raw string, c authorization.Context, global bool, expected uint64, reason string) error {
	if c.RequirePlatform() != nil {
		return ErrIAMContextInvalid
	}
	if expected == 0 || strings.TrimSpace(reason) == "" {
		return ErrIAMInvalidRelation
	}
	a, err := uc.authenticateSession(raw)
	if err != nil {
		return err
	}
	return uc.runtimeWrite(ctx, "session.revoke", a.SessionID, reason, a, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.IAMManagementWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = checkIAMIdentity(a, u, uc.now()); err != nil {
			return err
		}
		s, err := uc.iam.Session(ctx, tx, a.SessionID, c)
		if err != nil {
			return err
		}
		if err = checkIAMSession(a, s, uc.now()); err != nil {
			return err
		}
		return uc.iam.RevokeSession(ctx, tx, a.SessionID, c, global, uc.now(), expected)
	})
}

func (uc *IdentityUsecase) bindIAMOAuthIdentity(ctx context.Context, id int64, provider, oauthID string) (*User, error) {
	var saved User
	err := uc.runtimeWrite(ctx, "account.oauth.bind", strconv.FormatInt(id, 10), "legacy OAuth binding", authorization.Actor{ServiceID: "identity-legacy-account"}, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.LegacyAccountWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		saved, err = uc.iam.User(ctx, tx, id)
		if err != nil {
			return err
		}
		if saved.Status != UserStatusEnabled {
			return ErrUserDisabled
		}
		if saved.OAuthProvider == provider && saved.OAuthID != "" && saved.OAuthID != oauthID {
			return ErrOAuthAlreadyBound
		}
		identity, err := uc.iam.FindOAuthIdentityTx(ctx, tx, provider, oauthID)
		if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
			return err
		}
		if identity != nil && identity.UserID != id {
			return ErrOAuthAlreadyBound
		}
		own, err := uc.iam.FindOAuthIdentityByUserTx(ctx, tx, id, provider)
		if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
			return err
		}
		if own != nil && own.ProviderID != oauthID {
			return ErrOAuthAlreadyBound
		}
		if own != nil {
			return nil
		}
		_, err = uc.iam.CreateOAuthIdentity(ctx, tx, OAuthIdentity{UserID: id, Provider: provider, ProviderID: oauthID, CreatedAt: uc.now().Unix(), UpdatedAt: uc.now().Unix()})
		if err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = uc.iam.AdvanceUser(ctx, tx, id, rev); err != nil {
			return err
		}
		return uc.iam.AdvancePolicy(ctx, tx, p.PolicyRevision, false)
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// Audit account changes without password hashes, contact data or OAuth secrets.
func iamAccountAuditState(u User) map[string]any {
	return map[string]any{"user_id": u.ID, "status": u.Status, "legacy_role": u.Role, "password_epoch": u.PasswordChangedAt, "routing_access_revision": u.RoutingAccessRevision}
}
