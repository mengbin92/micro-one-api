package safecast

import (
	"math"
	"testing"
)

func TestIntToInt32(t *testing.T) {
	if got, err := IntToInt32(math.MaxInt32); err != nil || got != math.MaxInt32 {
		t.Fatalf("IntToInt32(MaxInt32) = %d, %v", got, err)
	}
	if _, err := IntToInt32(math.MaxInt32 + 1); err == nil {
		t.Fatal("IntToInt32 accepted overflow value")
	}
}

func TestIntToInt32Saturating(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want int32
	}{
		{"zero", 0, 0},
		{"first_attempt", 1, 1},
		{"max", math.MaxInt32, math.MaxInt32},
		{"overflow", math.MaxInt32 + 1, math.MaxInt32},
		{"max_int", math.MaxInt, math.MaxInt32},
		{"min", math.MinInt32, math.MinInt32},
		{"underflow", math.MinInt32 - 1, math.MinInt32},
		{"min_int", math.MinInt, math.MinInt32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IntToInt32Saturating(tc.in); got != tc.want {
				t.Fatalf("IntToInt32Saturating(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestInt64ToInt32(t *testing.T) {
	if got, err := Int64ToInt32(math.MinInt32); err != nil || got != math.MinInt32 {
		t.Fatalf("Int64ToInt32(MinInt32) = %d, %v", got, err)
	}
	if _, err := Int64ToInt32(int64(math.MinInt32) - 1); err == nil {
		t.Fatal("Int64ToInt32 accepted underflow value")
	}
}

func TestInt64ToUint(t *testing.T) {
	if got, err := Int64ToUint(0); err != nil || got != 0 {
		t.Fatalf("Int64ToUint(0) = %d, %v", got, err)
	}
	if _, err := Int64ToUint(-1); err == nil {
		t.Fatal("Int64ToUint accepted negative value")
	}
}

func TestInt32ToInt8(t *testing.T) {
	if got, err := Int32ToInt8(math.MaxInt8); err != nil || got != math.MaxInt8 {
		t.Fatalf("Int32ToInt8(MaxInt8) = %d, %v", got, err)
	}
	if _, err := Int32ToInt8(math.MaxInt8 + 1); err == nil {
		t.Fatal("Int32ToInt8 accepted overflow value")
	}
}

func TestUintToInt64(t *testing.T) {
	if got, err := UintToInt64(1); err != nil || got != 1 {
		t.Fatalf("UintToInt64(1) = %d, %v", got, err)
	}
}

func TestUintToUint32(t *testing.T) {
	if got, err := UintToUint32(math.MaxUint32); err != nil || got != math.MaxUint32 {
		t.Fatalf("UintToUint32(MaxUint32) = %d, %v", got, err)
	}
	if uint64(math.MaxUint) > math.MaxUint32 {
		if _, err := UintToUint32(uint(math.MaxUint32) + 1); err == nil {
			t.Fatal("UintToUint32 accepted overflow value")
		}
	}
}
