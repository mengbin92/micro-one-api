package xhttp

import (
	"strings"
	"testing"
)

func TestReadBodyRejectsOverLimit(t *testing.T) {
	if _, err := ReadBody(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("accepted oversized body")
	}
	b, err := ReadBody(strings.NewReader("1234"), 4)
	if err != nil || string(b) != "1234" {
		t.Fatalf("body=%s err=%v", b, err)
	}
}
