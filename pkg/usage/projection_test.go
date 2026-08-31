package usage

import (
	"math"
	"testing"
)

func TestProjectOpenAIInclusive(t *testing.T) {
	// The GLM-5.3 production shape from §2.7: exclusive Anthropic buckets
	// must project to an inclusive OpenAI usage whose total includes every
	// cache bucket.
	b := Buckets{
		UncachedInputTokens:   55619 - 7232,
		CacheReadTokens:       7232,
		CacheCreation5mTokens: 0,
		CacheCreation1hTokens: 0,
		OutputTokens:          261,
	}
	p := ProjectOpenAI(b)
	if p.PromptTokens != 55619 {
		t.Fatalf("PromptTokens = %d, want 55619", p.PromptTokens)
	}
	if p.TotalTokens != 55880 {
		t.Fatalf("TotalTokens = %d, want 55880 (input+output)", p.TotalTokens)
	}
	if p.CachedTokens != 7232 || p.CacheReadTokens != 7232 {
		t.Fatalf("cached details = %d/%d, want 7232/7232", p.CachedTokens, p.CacheReadTokens)
	}
	if b.BillableTotal() != 55880 {
		t.Fatalf("BillableTotal = %d, want 55880", b.BillableTotal())
	}
}

func TestProjectOpenAIIncludesAllCacheBuckets(t *testing.T) {
	b := Buckets{
		UncachedInputTokens:   100,
		CacheReadTokens:       1000,
		CacheCreation5mTokens: 200,
		CacheCreation1hTokens: 300,
		OutputTokens:          50,
	}
	p := ProjectOpenAI(b)
	if p.PromptTokens != 1600 {
		t.Fatalf("PromptTokens = %d, want 1600", p.PromptTokens)
	}
	if p.TotalTokens != 1650 {
		t.Fatalf("TotalTokens = %d, want 1650", p.TotalTokens)
	}
	if b.BillableTotal() != 1650 {
		t.Fatalf("BillableTotal = %d, want 1650", b.BillableTotal())
	}
}

func TestProjectOpenAIZero(t *testing.T) {
	p := ProjectOpenAI(Buckets{})
	if p.PromptTokens != 0 || p.OutputTokens != 0 || p.TotalTokens != 0 || p.CachedTokens != 0 {
		t.Fatalf("zero buckets projected to %+v, want all zero", p)
	}
}

func TestSplitInclusiveInverse(t *testing.T) {
	// ProjectOpenAI and SplitInclusive must be exact inverses for every
	// non-negative bucket combination: the two directions of the §4.3 matrix.
	b := Buckets{
		UncachedInputTokens:   111,
		CacheReadTokens:       222,
		CacheCreation5mTokens: 333,
		CacheCreation1hTokens: 44,
		OutputTokens:          55,
	}
	got := SplitInclusive(b.InclusiveInputTokens(), b.CacheReadTokens,
		b.CacheCreation5mTokens, b.CacheCreation1hTokens, b.OutputTokens)
	if got != b {
		t.Fatalf("SplitInclusive(ProjectOpenAI(b)) = %+v, want %+v", got, b)
	}
}

func TestSplitInclusiveClampsToZero(t *testing.T) {
	// cached exceeding the inclusive input clamps instead of going negative;
	// anomaly verdicts stay the parser's job.
	got := SplitInclusive(100, 45056, 0, 0, 9)
	if got.UncachedInputTokens != 0 {
		t.Fatalf("UncachedInputTokens = %d, want 0 (clamped)", got.UncachedInputTokens)
	}
	if got.CacheReadTokens != 45056 || got.OutputTokens != 9 {
		t.Fatalf("cache buckets altered: %+v", got)
	}
}

func TestSaturatingAdd(t *testing.T) {
	if got := addClamped(math.MaxInt64, 1); got != math.MaxInt64 {
		t.Fatalf("addClamped(MaxInt64, 1) = %d, want saturated MaxInt64", got)
	}
	if got := addClamped(1, 2, 3); got != 6 {
		t.Fatalf("addClamped(1,2,3) = %d, want 6", got)
	}
	if got := (Buckets{UncachedInputTokens: math.MaxInt64}).InclusiveTotalTokens(); got != math.MaxInt64 {
		t.Fatalf("InclusiveTotalTokens overflow = %d, want saturated MaxInt64", got)
	}
}
