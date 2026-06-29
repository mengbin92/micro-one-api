package credential

import "testing"

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"short", "abc", "***"},
		{"boundary8", "12345678", "********"},
		{"long", "sk-secret-access-token-value", "sk-s********************alue"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskSecret(c.in); got != c.want {
				t.Fatalf("MaskSecret(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMaskSecret_NeverLeaksFullToken(t *testing.T) {
	long := "sk-supersecret-0123456789abcdef"
	masked := MaskSecret(long)
	if masked == long {
		t.Fatalf("MaskSecret returned the cleartext token")
	}
}
