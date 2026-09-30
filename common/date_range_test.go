package common

import "testing"

func TestBeijingDateTime(t *testing.T) {
	if got := BeijingDateTime(0); got != "" {
		t.Fatalf("zero timestamp = %q, want empty", got)
	}
	if got := BeijingDateTime(1704067200); got != "2024-01-01 08:00:00" {
		t.Fatalf("formatted timestamp = %q", got)
	}
}
