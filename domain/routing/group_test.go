package routing

import (
	"reflect"
	"testing"
)

func TestGroups(t *testing.T) {
	got := Groups(" default, vip ,default,,VIP,team_one,team%two ")
	want := []string{"default", "vip", "VIP", "team_one", "team%two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Groups() = %v, want %v", got, want)
	}
}

func TestContainsGroup(t *testing.T) {
	for _, tt := range []struct {
		membership, group string
		want              bool
	}{
		{"default, vip,default", "vip", true},
		{"default,vip", "default", true},
		{"default,svip", "vip", false},
		{"default,VIP", "vip", false},
		{"team_one,team%two", "team_one", true},
		{"team_one,team%two", "team%two", true},
		{"default,vip", "default,vip", false},
		{"default,vip", " vip ", false},
		{"default,vip", "*", false},
		{"", "default", false},
		{"", "", false},
	} {
		t.Run(tt.membership+"/"+tt.group, func(t *testing.T) {
			if got := ContainsGroup(tt.membership, tt.group); got != tt.want {
				t.Fatalf("ContainsGroup(%q, %q) = %v, want %v", tt.membership, tt.group, got, tt.want)
			}
		})
	}
}
