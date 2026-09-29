package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCoverageSuitesUseDisjointTestTargetsAndProfiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		profile string
	}{
		{"replay", []string{"./tests/replay/..."}, "coverage.replay.out"},
		{"unit", []string{"./pkg/...", "./internal/..."}, "coverage.unit.out"},
		{"integration", []string{"-tags=integration", "./tests/integration/..."}, "coverage.integration.out"},
		{"combined", []string{"./tests/replay/...", "./pkg/...", "./internal/..."}, "coverage.combined.out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := suiteSpecFor(tc.name)
			if err != nil || got.profile != tc.profile || !reflect.DeepEqual(got.args, tc.args) {
				t.Fatalf("suite = %+v, %v", got, err)
			}
		})
	}

	{
		_, err := suiteSpecFor("unknown")
		if err == nil {
			t.Fatal("unknown suite was accepted")
		}
	}
}

func TestTotalsMergeDuplicatesAndCountStatements(t *testing.T) {
	t.Parallel()

	covered, total, err := totals([]byte("mode: atomic\na.go:1.1,2.1 3 0\na.go:1.1,2.1 3 4\nb.go:1.1,4.1 7 0\n"))
	if err != nil || covered != 3 || total != 10 {
		t.Fatalf("got %d/%d: %v", covered, total, err)
	}
}

func TestExcludeGeneratedCodeKeepsHandwrittenPackageCoverage(t *testing.T) {
	t.Parallel()

	profile := []byte(
		"mode: atomic\n" +
			"github.com/example/pkg/ringapimodels/models.gen.go:1.1,2.1 8 0\n" +
			"github.com/example/internal/generatedhttp/client.gen.go:1.1,2.1 12 0\n" +
			"github.com/example/internal/generatedfcm/client.gen.go:1.1,2.1 14 0\n" +
			"github.com/example/internal/generatedsignaling/server_frame.go:1.1,2.1 16 0\n" +
			"github.com/example/pkg/generatedhttp/client.gen.go:1.1,2.1 18 0\n" +
			"github.com/example/pkg/generatedsignaling/server_frame.go:1.1,2.1 20 0\n" +
			"github.com/example/pkg/ringapimodels/devices.go:1.1,2.1 2 1\n",
	)
	filtered := excludeGeneratedCode(profile)

	covered, total, err := totals(filtered)
	if err != nil || covered != 2 || total != 2 {
		t.Fatalf("filtered coverage = %d/%d, %v", covered, total, err)
	}
}

func TestGeneratedPackagesAreExcludedFromMaintainedCoverage(t *testing.T) {
	t.Parallel()

	for _, packageName := range []string{
		"github.com/example/internal/generatedhttp",
		"github.com/example/internal/generatedsignaling",
		"github.com/example/internal/generatedfcm",
		"github.com/example/pkg/generatedhttp",
		"github.com/example/pkg/generatedsignaling",
		"github.com/example/pkg/generatedfcm",
	} {
		if !isGeneratedPackage(packageName) {
			t.Errorf("generated package %s was not excluded", packageName)
		}
	}

	if isGeneratedPackage("github.com/example/pkg/ring") {
		t.Fatal("handwritten ring package was excluded")
	}
}

func TestPackageTotalsMergeBlocksByPackage(t *testing.T) {
	t.Parallel()

	profile := []byte(
		"mode: atomic\n" +
			"github.com/example/a/one.go:1.1,2.1 3 0\n" +
			"github.com/example/a/two.go:1.1,2.1 1 1\n" +
			"github.com/example/b/one.go:1.1,2.1 4 0\n",
	)

	got, err := packageTotals(profile)
	if err != nil {
		t.Fatal(err)
	}

	if got["github.com/example/a"] != 25 || got["github.com/example/b"] != 0 {
		t.Fatalf("unexpected package coverage: %#v", got)
	}
}

func TestCheckPackageFloorsRequiresExactPackageSetAndRejectsRegression(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "floors.json")

	writeErr := os.WriteFile(path, []byte(`{"a": 60, "b": 0}`), 0600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	equalErr := checkPackageFloors(map[string]float64{"a": 60, "b": 3}, path)
	if equalErr != nil {
		t.Fatalf("equal floors should pass: %v", equalErr)
	}

	regressionErr := checkPackageFloors(map[string]float64{"a": 59.9, "b": 3}, path)
	if regressionErr == nil {
		t.Fatal("coverage regression below floor passed")
	}

	missingErr := checkPackageFloors(map[string]float64{"a": 60}, path)
	if missingErr == nil {
		t.Fatal("missing baseline package passed")
	}

	untrackedErr := checkPackageFloors(map[string]float64{"a": 60, "b": 3, "c": 99}, path)
	if untrackedErr == nil {
		t.Fatal("untracked package passed")
	}
}
func TestTotalsRejectInvalidOrEmptyProfiles(t *testing.T) {
	t.Parallel()

	for _, profile := range []string{
		"",
		"mode: atomic\n",
		"mode: atomic\nbad\n",
		"mode: atomic\na 2 -1\n",
		"mode: atomic\na -1 1\n",
		"mode: atomic\na 1 0\na 2 1\n",
	} {
		{
			_, _, err := totals([]byte(profile))
			if err == nil {
				t.Fatalf("accepted %q", profile)
			}
		}
	}
}
