package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingNotifier struct {
	calls []notifierCall
	err   error
}

type notifierCall struct {
	notifyType string
	recipient  string
	subject    string
	content    string
}

func (r *recordingNotifier) CreateNotification(_ context.Context, notifyType, recipient, subject, content string) error {
	r.calls = append(r.calls, notifierCall{notifyType, recipient, subject, content})
	return r.err
}

func newJobHarness(t *testing.T, repo *mockReconRepo, notifier Notifier, recipients []string, notifyType string) *ReconciliationJob {
	t.Helper()
	uc := NewReconciliationUsecase(
		&mockAccountRepo{account: &Account{UserID: "user1", Quota: 1000, Group: "default"}},
		&mockReservationRepo{reservations: map[string]*Reservation{}},
		repo,
		nil,
	)
	opts := []JobOption{}
	if notifier != nil {
		opts = append(opts, WithNotifier(notifier))
	}
	if recipients != nil {
		opts = append(opts, WithRecipients(recipients))
	}
	if notifyType != "" {
		opts = append(opts, WithNotifyType(notifyType))
	}
	return NewReconciliationJob(uc, time.Hour, opts...)
}

func TestReconciliationJob_NoDiscrepanciesNoNotify(t *testing.T) {
	repo := &mockReconRepo{
		accounts:   []*Account{{UserID: "user1", Quota: 1000, Group: "default"}},
		ledgerSums: map[string]int64{"user1": 1000},
	}
	n := &recordingNotifier{}
	job := newJobHarness(t, repo, n, []string{"admin"}, "")

	job.runReconciliation(context.Background())

	assert.Empty(t, n.calls, "notifier should not be called when there are no discrepancies")
}

func TestReconciliationJob_NilNotifierStillWorks(t *testing.T) {
	repo := &mockReconRepo{
		accounts:   []*Account{{UserID: "user1", Quota: 1000, Group: "default"}},
		ledgerSums: map[string]int64{"user1": 500}, // diff=500 > 100
	}
	job := newJobHarness(t, repo, nil, nil, "")
	assert.NotPanics(t, func() {
		job.runReconciliation(context.Background())
	})
}

func TestReconciliationJob_DiscrepancyTriggersNotify(t *testing.T) {
	repo := &mockReconRepo{
		accounts:   []*Account{{UserID: "user1", Quota: 1000, Group: "default"}},
		ledgerSums: map[string]int64{"user1": 500},
		channels: []*ChannelUsageSnapshot{
			{ChannelID: 10, UsedQuota: 200},
		},
		channelLedgerUsage: []*ChannelLedgerUsage{
			{ChannelID: 10, Quota: 600, UpstreamCost: 50},
		},
		ledgerConsumeSummary: &ConsumeSummary{Count: 5, Quota: 1000},
		logConsumeSummary:    &ConsumeSummary{Count: 3, Quota: 800},
	}
	n := &recordingNotifier{}
	job := newJobHarness(t, repo, n, []string{"admin", "ops"}, "webhook")

	job.runReconciliation(context.Background())

	require.Len(t, n.calls, 2, "one notification per recipient")
	for _, call := range n.calls {
		assert.Equal(t, "webhook", call.notifyType)
		assert.Contains(t, []string{"admin", "ops"}, call.recipient)
	}
	first := n.calls[0]
	assert.Contains(t, first.subject, "[recon]")
	assert.Contains(t, first.subject, "3 discrepancies")
	assert.Contains(t, first.content, "Account quota mismatches")
	assert.Contains(t, first.content, "Channel usage mismatches")
	assert.Contains(t, first.content, "Ledger/log consume drift")
}

func TestReconciliationJob_NotifierErrorDoesNotPanic(t *testing.T) {
	repo := &mockReconRepo{
		accounts:   []*Account{{UserID: "user1", Quota: 1000, Group: "default"}},
		ledgerSums: map[string]int64{"user1": 500},
	}
	n := &recordingNotifier{err: errors.New("downstream boom")}
	job := newJobHarness(t, repo, n, []string{"admin"}, "")

	assert.NotPanics(t, func() {
		job.runReconciliation(context.Background())
	})
}

func TestReconciliationJob_DefaultRecipientsWhenNoneConfigured(t *testing.T) {
	repo := &mockReconRepo{
		accounts:   []*Account{{UserID: "user1", Quota: 1000, Group: "default"}},
		ledgerSums: map[string]int64{"user1": 500},
	}
	n := &recordingNotifier{}
	// Pass no WithRecipients option to keep the constructor default fallback.
	job := newJobHarness(t, repo, n, nil, "")

	job.runReconciliation(context.Background())

	require.Len(t, n.calls, 1)
	assert.Equal(t, "", n.calls[0].recipient)
	assert.Equal(t, "event", n.calls[0].notifyType)
}

func TestReconciliationJob_WebhookFallbackRecipient(t *testing.T) {
	repo := &mockReconRepo{
		accounts:   []*Account{{UserID: "user1", Quota: 1000, Group: "default"}},
		ledgerSums: map[string]int64{"user1": 500},
	}
	n := &recordingNotifier{}
	job := newJobHarness(t, repo, n, []string{""}, "webhook")

	job.runReconciliation(context.Background())

	require.Len(t, n.calls, 1)
	assert.Equal(t, "webhook", n.calls[0].notifyType)
	assert.Equal(t, "", n.calls[0].recipient)
	assert.Contains(t, n.calls[0].subject, "[recon]")
}
