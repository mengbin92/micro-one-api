package credential

// MaskSecret returns a redacted view of a secret suitable for inclusion in API
// responses and logs. It keeps the first 4 and last 4 characters and replaces
// the middle with asterisks; secrets of 8 characters or fewer are fully masked.
//
// This exists so the admin/management surfaces never return OAuth access or
// refresh tokens in cleartext. The real (decrypted) token is only ever read
// from storage on the relay path that needs to call the upstream.
func MaskSecret(secret string) string {
	if len(secret) <= 8 {
		return repeatStar(len(secret))
	}
	head := secret[:4]
	tail := secret[len(secret)-4:]
	return head + repeatStar(len(secret)-8) + tail
}

func repeatStar(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = '*'
	}
	return string(b)
}
