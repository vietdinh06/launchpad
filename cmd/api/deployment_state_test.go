package main

import "testing"

func TestValidTransition(t *testing.T) {
	tests := []struct {
		from    string
		to      string
		allowed bool
	}{
		{"pending", "running", true},
		{"running", "succeeded", true},
		{"running", "failed", true},
		{"pending", "succeeded", false},
		{"succeeded", "running", false},
		{"failed", "pending", false},
		{"failed", "running", false},
	}

	for _, tt := range tests {
		got := validTransition(tt.from, tt.to)

		if got != tt.allowed {
			t.Errorf(
				"%s -> %s: got %v, want %v",
				tt.from, tt.to, got, tt.allowed,
			)
		}
	}
}
