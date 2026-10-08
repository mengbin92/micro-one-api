package main

import "testing"

func TestAccountOpsShardFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, count, index   string
		wantCount, wantIndex int64
		wantError            bool
	}{
		{name: "default", wantCount: 1},
		{name: "sharded", count: "3", index: "2", wantCount: 3, wantIndex: 2},
		{name: "missing index", count: "3", wantError: true},
		{name: "zero count", count: "0", wantError: true},
		{name: "negative count", count: "-1", wantError: true},
		{name: "negative index", count: "2", index: "-1", wantError: true},
		{name: "index out of range", count: "2", index: "2", wantError: true},
		{name: "invalid count", count: "oops", wantError: true},
		{name: "invalid index", count: "2", index: "oops", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUBSCRIPTION_ACCOUNT_OPS_SHARD_COUNT", tc.count)
			t.Setenv("SUBSCRIPTION_ACCOUNT_OPS_SHARD_INDEX", tc.index)
			got, err := accountOpsShardFromEnv()
			if (err != nil) != tc.wantError {
				t.Fatalf("shard error = %v, wantError = %v", err, tc.wantError)
			}
			if err == nil && (got.Count != tc.wantCount || got.Index != tc.wantIndex) {
				t.Fatalf("shard = %+v, want index=%d count=%d", got, tc.wantIndex, tc.wantCount)
			}
		})
	}
}
