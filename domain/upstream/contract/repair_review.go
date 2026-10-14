package contract

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"

	"micro-one-api/pkg/jsonx"
)

// RepairReview is an offline reviewer's authorization for exactly one finite
// contract. Candidate generators receive no reviewer key or production client.
// Price, billing mode and model authorization are outside this signature.
type RepairReview struct {
	ProposalID       string `json:"proposal_id"`
	BaseRevision     string `json:"base_revision"`
	BindingRevision  int64  `json:"binding_revision"`
	IncidentRevision int64  `json:"incident_revision"`
	SourceRevision   string `json:"source_revision"`
	ReplayID         string `json:"replay_id"`
	CIEvidenceID     string `json:"ci_evidence_id"`
	Reviewer         string `json:"reviewer"`
	KeyID            string `json:"key_id"`
	ExpiresAt        int64  `json:"expires_at"`
	Signature        string `json:"signature,omitempty"`
}

func repairPayload(d Document) ([]byte, error) {
	if d.Repair == nil {
		return nil, fmt.Errorf("repair review absent")
	}
	r := *d.Repair
	r.Signature = ""
	d.Repair = &r
	return jsonx.Marshal(struct {
		Domain   string
		Document Document
	}{"micro-one-api/upstream-repair-review/v1", d})
}

func SignRepairReview(d Document, key ed25519.PrivateKey) (Document, error) {
	if len(key) != ed25519.PrivateKeySize {
		return d, fmt.Errorf("invalid reviewer key")
	}
	payload, err := repairPayload(d)
	if err != nil {
		return d, err
	}
	r := *d.Repair
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))
	d.Repair = &r
	return d, nil
}

func VerifyRepairReview(d Document, keys map[string]string, now time.Time) error {
	r := d.Repair
	if r == nil {
		return nil
	} // Existing operator-authored P2 contracts remain valid.
	if len(r.ProposalID) != 64 || len(r.ReplayID) != 64 || r.BaseRevision != "" && len(r.BaseRevision) != 64 || r.BindingRevision < 0 || r.IncidentRevision < 0 || r.SourceRevision == "" || r.Reviewer == "" || r.CIEvidenceID == "" || r.ExpiresAt <= now.Unix() || r.ExpiresAt > now.Add(24*time.Hour).Unix() {
		return fmt.Errorf("fixed repair scope, independent replay/CI and unexpired review required")
	}
	key, err := base64.StdEncoding.DecodeString(keys[r.KeyID])
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("untrusted repair reviewer")
	}
	sig, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("repair review signature required")
	}
	payload, err := repairPayload(d)
	if err != nil || !ed25519.Verify(key, payload, sig) {
		return fmt.Errorf("repair review content changed")
	}
	return nil
}
