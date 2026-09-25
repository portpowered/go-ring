package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTotalsMergeDuplicatesAndCountStatements(t *testing.T) {
	covered, total, err := totals([]byte("mode: atomic\na.go:1.1,2.1 3 0\na.go:1.1,2.1 3 4\nb.go:1.1,4.1 7 0\n"))
	if err != nil || covered != 3 || total != 10 {
		t.Fatalf("got %d/%d: %v", covered, total, err)
	}
}

func TestExcludeGeneratedModelsKeepsHandwrittenPackageCoverage(t *testing.T) {
	profile := []byte("mode: atomic\ngithub.com/example/pkg/ringapimodels/models.gen.go:1.1,2.1 8 0\ngithub.com/example/pkg/ringapimodels/devices.go:1.1,2.1 2 1\n")
	filtered := excludeGeneratedModels(profile)
	covered, total, err := totals(filtered)
	if err != nil || covered != 2 || total != 2 {
		t.Fatalf("filtered coverage = %d/%d, %v", covered, total, err)
	}
}

func TestPackageTotalsMergeBlocksByPackage(t *testing.T) {
	profile := []byte("mode: atomic\ngithub.com/example/a/one.go:1.1,2.1 3 0\ngithub.com/example/a/two.go:1.1,2.1 1 1\ngithub.com/example/b/one.go:1.1,2.1 4 0\n")
	got, err := packageTotals(profile)
	if err != nil {
		t.Fatal(err)
	}
	if got["github.com/example/a"] != 25 || got["github.com/example/b"] != 0 {
		t.Fatalf("unexpected package coverage: %#v", got)
	}
}

func TestCheckPackageFloorsRequiresExactPackageSetAndRejectsRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "floors.json")
	if err := os.WriteFile(path, []byte(`{"a": 60, "b": 0}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPackageFloors(map[string]float64{"a": 60, "b": 3}, path); err != nil {
		t.Fatalf("equal floors should pass: %v", err)
	}
	if err := checkPackageFloors(map[string]float64{"a": 59.9, "b": 3}, path); err == nil {
		t.Fatal("coverage regression below floor passed")
	}
	if err := checkPackageFloors(map[string]float64{"a": 60}, path); err == nil {
		t.Fatal("missing baseline package passed")
	}
	if err := checkPackageFloors(map[string]float64{"a": 60, "b": 3, "c": 99}, path); err == nil {
		t.Fatal("untracked package passed")
	}
}
func TestTotalsRejectInvalidOrEmptyProfiles(t *testing.T) {
	for _, profile := range []string{"", "mode: atomic\n", "mode: atomic\nbad\n", "mode: atomic\na 2 -1\n", "mode: atomic\na -1 1\n", "mode: atomic\na 1 0\na 2 1\n"} {
		if _, _, err := totals([]byte(profile)); err == nil {
			t.Fatalf("accepted %q", profile)
		}
	}
}
