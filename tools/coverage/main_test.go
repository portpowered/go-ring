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
		name            string
		args            []string
		additionalTests []coverageTestRun
		profile         string
	}{
		{
			name: "replay", args: []string{"./tests/replay/..."},
			additionalTests: []coverageTestRun{{
				args: []string{
					"-run=^TestPublicSessionExpirySendsCloseAndClosesReplaySocket$",
					"./pkg/ring",
				},
				coverPackages: nil,
			}, {
				args: []string{
					"-run=^(TestBatteryReadingUnmarshalJSON|TestOwnerIDUnmarshalJSON)$",
					"./pkg/ringtypes",
				},
				coverPackages: []string{"github.com/portpowered/go-ring/pkg/ringtypes"},
			}, {
				args: []string{
					"-run=^(TestEmptyDetailErrorsRemainClassifiedAndSafe|TestTokenErrorPreservesCause)$",
					"./internal/ringerrors",
				},
				coverPackages: nil,
			}},
			profile: "coverage.replay.out",
		},
		{
			name: "unit", args: []string{"./pkg/...", "./internal/..."},
			additionalTests: nil, profile: "coverage.unit.out",
		},
		{
			name: "integration", args: []string{"-tags=integration", "./tests/integration/..."},
			additionalTests: nil, profile: "coverage.integration.out",
		},
		{
			name: "combined", args: []string{"./tests/replay/...", "./pkg/...", "./internal/..."},
			additionalTests: nil, profile: "coverage.combined.out",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := suiteSpecFor(tc.name)
			if err != nil || got.profile != tc.profile || !reflect.DeepEqual(got.args, tc.args) ||
				!reflect.DeepEqual(got.additionalTests, tc.additionalTests) {
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

func TestAppendCoverageProfileMergesRecordsWithMatchingMode(t *testing.T) {
	t.Parallel()

	profilePath := filepath.Join(t.TempDir(), "primary.out")
	additionalPath := filepath.Join(t.TempDir(), "additional.out")
	primary := []byte("mode: atomic\na.go:1.1,2.1 3 0\nb.go:1.1,2.1 2 1\n")
	additional := []byte("mode: atomic\na.go:1.1,2.1 3 1\nc.go:1.1,2.1 4 0\n")

	writeErr := os.WriteFile(profilePath, primary, 0600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	writeErr = os.WriteFile(additionalPath, additional, 0600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	appendErr := appendCoverageProfile(profilePath, additionalPath)
	if appendErr != nil {
		t.Fatal(appendErr)
	}

	merged, readErr := os.ReadFile(profilePath) // #nosec G304 -- profilePath is created by t.TempDir above.
	if readErr != nil {
		t.Fatal(readErr)
	}

	covered, total, totalsErr := totals(merged)
	if totalsErr != nil || covered != 5 || total != 9 {
		t.Fatalf("merged coverage = %d/%d, %v", covered, total, totalsErr)
	}

	badModePath := filepath.Join(t.TempDir(), "incompatible.out")

	writeErr = os.WriteFile(badModePath, []byte("mode: set\na.go:1.1,2.1 3 1\n"), 0600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	appendErr = appendCoverageProfile(profilePath, badModePath)
	if appendErr == nil {
		t.Fatal("incompatible coverage modes were accepted")
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
			"github.com/example/pkg/dependencymodels/fcm/models.gen.go:1.1,2.1 22 0\n" +
			"github.com/example/pkg/dependencymodels/rest/models.gen.go:1.1,2.1 24 0\n" +
			"github.com/example/pkg/dependencymodels/signaling/live_view_body.go:1.1,2.1 26 0\n" +
			"github.com/example/pkg/ringtypes/types.go:1.1,2.1 5 1\n" +
			"github.com/example/pkg/ringapimodels/devices.go:1.1,2.1 2 1\n",
	)
	filtered := excludeGeneratedCode(profile)

	covered, total, err := totals(filtered)
	if err != nil || covered != 7 || total != 7 {
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
		"github.com/example/pkg/dependencymodels/fcm",
		"github.com/example/pkg/dependencymodels/rest",
		"github.com/example/pkg/dependencymodels/signaling",
	} {
		if !isGeneratedPackage(packageName) {
			t.Errorf("generated package %s was not excluded", packageName)
		}
	}

	for _, packageName := range []string{
		"github.com/example/pkg/ring",
		"github.com/example/pkg/ringtypes",
		"github.com/example/pkg/ringapimodels",
	} {
		if isGeneratedPackage(packageName) {
			t.Fatalf("handwritten package %s was excluded", packageName)
		}
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
