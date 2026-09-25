package main

import "testing"

func TestTotalsMergeDuplicatesAndCountStatements(t *testing.T) {
	covered, total, err := totals([]byte("mode: atomic\na.go:1.1,2.1 3 0\na.go:1.1,2.1 3 4\nb.go:1.1,4.1 7 0\n"))
	if err != nil || covered != 3 || total != 10 {
		t.Fatalf("got %d/%d: %v", covered, total, err)
	}
}
func TestTotalsRejectInvalidOrEmptyProfiles(t *testing.T) {
	for _, profile := range []string{"", "mode: atomic\n", "mode: atomic\nbad\n", "mode: atomic\na 2 -1\n", "mode: atomic\na -1 1\n", "mode: atomic\na 1 0\na 2 1\n"} {
		if _, _, err := totals([]byte(profile)); err == nil {
			t.Fatalf("accepted %q", profile)
		}
	}
}
