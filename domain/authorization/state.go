package authorization

import (
	"errors"
	"time"
)

var ErrCutover = errors.New("authorization write blocked by mode/cutover state")

type PolicyState struct {
	Mode            string
	Cutover         string
	BatchID         string
	VerifiedAt      *time.Time
	PolicyRevision  uint64
	CatalogRevision uint64
}

type WriteKind string

const (
	LegacyAccountWrite WriteKind = "legacy_account"
	IAMManagementWrite WriteKind = "iam_management"
	CandidateWrite     WriteKind = "candidate"
	MigrationWrite     WriteKind = "migration"
	BootstrapWrite     WriteKind = "bootstrap"
)

func (s PolicyState) Validate() error {
	valid := (s.Mode == "legacy" && (s.Cutover == "idle" || s.Cutover == "blocked")) || (s.Mode == "iam" && (s.Cutover == "verified" || s.Cutover == "complete"))
	if !valid || (s.Cutover != "idle" && s.BatchID == "") || (s.Mode == "iam" && (s.VerifiedAt == nil || s.VerifiedAt.IsZero())) {
		return ErrCutover
	}
	if (s.Cutover == "idle" && s.BatchID != "") || (s.Mode == "legacy" && s.VerifiedAt != nil) {
		return ErrCutover
	}
	return nil
}

// CheckWrite is only a state gate, not an authorization decision. Migration
// identity, reason/CAS and protected invariants must still be checked by biz.
func (s PolicyState) CheckWrite(kind WriteKind, migrationIdentity bool) error {
	if err := s.Validate(); err != nil {
		return err
	}
	switch kind {
	case LegacyAccountWrite:
		if s.Mode == "legacy" && s.Cutover == "idle" {
			return nil
		}
	case IAMManagementWrite:
		if s.Mode == "iam" && s.Cutover == "complete" {
			return nil
		}
	case CandidateWrite:
		if migrationIdentity && s.Mode == "legacy" && s.Cutover == "idle" {
			return nil
		}
	case MigrationWrite:
		if migrationIdentity && (s.Cutover == "blocked" || s.Cutover == "verified") {
			return nil
		}
	case BootstrapWrite:
		if s.Cutover == "idle" || s.Cutover == "complete" {
			return nil
		}
	}
	return ErrCutover
}

func ValidateOrigin(origin, batch string) error {
	switch origin {
	case "legacy_candidate":
		if batch != "" {
			return nil
		}
	case "default", "bootstrap", "explicit":
		if batch == "" {
			return nil
		}
	}
	return ErrCutover
}
