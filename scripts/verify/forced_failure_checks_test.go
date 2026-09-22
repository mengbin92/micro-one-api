package main

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestVerifySubscriptionProjectionAttribution(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE billing_ledgers (
		reference_id TEXT, cost_source TEXT, type TEXT, ledger_dedupe_key TEXT,
		amount INTEGER, source_kind TEXT, channel_id INTEGER, subscription_account_id INTEGER
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO billing_ledgers VALUES
		('subscription-res', 'subscription', 'consume', 'subscription-res:consume:subscription', -8, 'subscription', 5, 5),
		('channel-res', 'subscription', 'consume', 'channel-res:consume:subscription', -4, 'channel', 1, 0),
		('wrong-source-res', 'subscription', 'consume', 'wrong-source-res:consume:subscription', -8, 'channel', 5, 5)`)
	if err != nil {
		t.Fatal(err)
	}
	if !verify(db, "subscription-res", "subscription", 8, true) {
		t.Fatal("subscription projection with channel_id should pass")
	}
	if !verify(db, "channel-res", "channel", 4, true) {
		t.Fatal("ordinary channel attribution should pass")
	}
	if verify(db, "wrong-source-res", "subscription", 8, true) {
		t.Fatal("mismatched source_kind should fail even when subscription_account_id is set")
	}
}
