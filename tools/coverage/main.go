// Command coverage measures maintained library code by the test suite that
// exercises it. Replay is the primary compatibility gate.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	suiteIntegrationName     = "integration"
	suiteCombinedName        = "combined"
	coverageRecordFieldCount = 3
)

type coverageError string

func (e coverageError) Error() string { return string(e) }

type coverageCauseError struct {
	operation string
	cause     error
}

func (e coverageCauseError) Error() string { return e.operation + ": " + e.cause.Error() }
func (e coverageCauseError) Unwrap() error { return e.cause }

func main() {
	if err := runCoverage(); err != nil {
		fail(err)
	}
}

func runCoverage() error {
	suite := flag.String("suite", "replay", "test suite: replay, unit, integration, or combined")
	minimum := flag.Float64("minimum", -1, "override the suite's minimum statement coverage")
	baselinePath := flag.String("baselines", "tools/coverage/baselines.json", "per-package minimum coverage floors")
	flag.Parse()
	spec, err := suiteSpecFor(*suite)
	if err != nil {
		return err
	}
	if *minimum != -1 {
		spec.minimum = *minimum
	}
	if spec.minimum < 0 || spec.minimum > 100 {
		return coverageError("minimum must be between zero and 100")
	}
	if err := os.Setenv("GOWORK", "off"); err != nil {
		return err
	}
	packages, err := maintainedPackages()
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return coverageError("no maintained library packages found")
	}
	if spec.name == suiteIntegrationName && os.Getenv("RING_ACCESS_TOKEN") == "" {
		return coverageError("integration coverage requires RING_ACCESS_TOKEN; otherwise live tests skip and the report is misleading")
	}
	return calculateCoverage(spec, *baselinePath, packages)
}

func maintainedPackages() ([]string, error) {
	output, err := exec.Command("go", "list", "./pkg/...", "./internal/...").Output()
	if err != nil {
		return nil, err
	}
	packages := make([]string, 0)
	for _, name := range strings.Fields(string(output)) {
		if !strings.Contains(name, "/internal/testkit/") && !strings.HasSuffix(name, "/internal/testkit") &&
			!strings.HasSuffix(name, "/pkg/generatedhttp") && !strings.HasSuffix(name, "/pkg/generatedsignaling") {
			packages = append(packages, name)
		}
	}
	return packages, nil
}

func calculateCoverage(spec suiteSpec, baselinePath string, packages []string) error {
	profileFile, err := os.CreateTemp(".", "coverage.*.out")
	if err != nil {
		return err
	}
	profilePath := profileFile.Name()
	if err = profileFile.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(profilePath) }()
	args := []string{"test", "-race", "-coverpkg=" + strings.Join(packages, ","), "-coverprofile=" + profilePath, "-covermode=atomic"}
	args = append(args, spec.args...)
	args = append(args, "-timeout", spec.timeout)
	cmd := exec.Command("go", args...) // #nosec G204 -- args are built from fixed local suite definitions.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		return err
	}
	profile, err := os.ReadFile(profilePath) // #nosec G304 -- profilePath was created by os.CreateTemp above.
	if err != nil {
		return err
	}
	profile = excludeGeneratedModels(profile)
	if err = os.WriteFile(profilePath, profile, 0o600); err != nil {
		return err
	}
	covered, total, err := totals(profile)
	if err != nil {
		return err
	}
	// Publish only the completed profile; parallel runs cannot corrupt each
	// other's instrumentation or coverage calculation.
	if err = os.Rename(profilePath, spec.profile); err != nil {
		return err
	}
	percent := 100 * float64(covered) / float64(total)
	fmt.Printf("%s coverage: %.2f%% (%d/%d maintained statements); required %.2f%%; profile %s\n", spec.name, percent, covered, total, spec.minimum, spec.profile)
	fmt.Println("Includes maintained pkg and internal; excludes generated models/wire code, testkit, examples, tests, and tools.")
	if percent < spec.minimum {
		return coverageError(fmt.Sprintf("%s coverage target not met", spec.name))
	}
	packageCoverage, err := packageTotals(profile)
	if err != nil {
		return err
	}
	if spec.name == suiteCombinedName {
		if err = checkPackageFloors(packageCoverage, baselinePath); err != nil {
			return err
		}
	} else {
		printPackageCoverage(packageCoverage)
	}
	return nil
}

type suiteSpec struct {
	name    string
	args    []string
	profile string
	minimum float64
	timeout string
}

func suiteSpecFor(name string) (suiteSpec, error) {
	switch name {
	case "replay":
		return suiteSpec{name, []string{"./tests/replay/..."}, "coverage.replay.out", 85, "120s"}, nil
	case "unit":
		return suiteSpec{name, []string{"./pkg/...", "./internal/..."}, "coverage.unit.out", 50, "120s"}, nil
	case suiteIntegrationName:
		return suiteSpec{name, []string{"-tags=integration", "./tests/integration/..."}, "coverage.integration.out", 0, "5m"}, nil
	case suiteCombinedName:
		return suiteSpec{name, []string{"./tests/replay/...", "./pkg/...", "./internal/..."}, "coverage.combined.out", 90, "120s"}, nil
	default:
		return suiteSpec{}, coverageError(fmt.Sprintf("unknown suite %q", name))
	}
}

func printPackageCoverage(coverage map[string]float64) {
	names := make([]string, 0, len(coverage))
	for name := range coverage {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  %s: %.1f%%\n", name, coverage[name])
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

// Keep the generated SDK projection out of the maintained handwritten-code
// denominator and the published profile. The same package also contains
// handwritten device methods, which remain in scope.
func excludeGeneratedModels(profile []byte) []byte {
	lines := bytes.Split(profile, []byte{'\n'})
	filtered := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if !bytes.Contains(line, []byte("/pkg/ringapimodels/models.gen.go:")) {
			filtered = append(filtered, line)
		}
	}
	return bytes.Join(filtered, []byte{'\n'})
}

func totals(profile []byte) (covered, total int, err error) {
	blocks, err := parseBlocks(profile)
	if err != nil {
		return 0, 0, err
	}
	for _, block := range blocks {
		total += block.statements
		if block.hit {
			covered += block.statements
		}
	}
	if total == 0 {
		return 0, 0, coverageError("coverage profile contains no statements")
	}
	return covered, total, nil
}

type coverageBlock struct {
	statements int
	hit        bool
}

func parseBlocks(profile []byte) (map[string]coverageBlock, error) {
	scan := bufio.NewScanner(bytes.NewReader(profile))
	if !scan.Scan() || !strings.HasPrefix(scan.Text(), "mode: ") {
		return nil, coverageError("missing coverage mode")
	}
	// Merge duplicate blocks defensively; repeated records must not inflate coverage.
	blocks := map[string]coverageBlock{}
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) != coverageRecordFieldCount {
			return nil, coverageError("invalid coverage record")
		}
		statements, e := strconv.Atoi(fields[1])
		if e != nil || statements < 0 {
			return nil, coverageError("invalid statement count")
		}
		count, e := strconv.ParseUint(fields[2], 10, 64)
		if e != nil {
			return nil, coverageError("invalid execution count")
		}
		previous, exists := blocks[fields[0]]
		if exists && previous.statements != statements {
			return nil, coverageError("inconsistent coverage block")
		}
		blocks[fields[0]] = coverageBlock{statements, previous.hit || count > 0}
	}
	if scan.Err() != nil {
		return nil, scan.Err()
	}
	return blocks, nil
}

func packageTotals(profile []byte) (map[string]float64, error) {
	blocks, err := parseBlocks(profile)
	if err != nil {
		return nil, err
	}
	type counts struct{ covered, total int }
	byPackage := map[string]counts{}
	for path, block := range blocks {
		separator := strings.LastIndex(path, "/")
		if separator < 0 {
			return nil, coverageError(fmt.Sprintf("coverage path has no package: %s", path))
		}
		pkg := path[:separator]
		entry := byPackage[pkg]
		entry.total += block.statements
		if block.hit {
			entry.covered += block.statements
		}
		byPackage[pkg] = entry
	}
	out := map[string]float64{}
	for pkg, count := range byPackage {
		if count.total > 0 {
			out[pkg] = 100 * float64(count.covered) / float64(count.total)
		}
	}
	return out, nil
}

func checkPackageFloors(actual map[string]float64, path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is the operator-provided --baselines option.
	if err != nil {
		return coverageCauseError{operation: "read per-package coverage floors", cause: err}
	}
	floors := map[string]float64{}
	if err = json.Unmarshal(data, &floors); err != nil {
		return coverageCauseError{operation: "parse per-package coverage floors", cause: err}
	}
	for pkg, floor := range floors {
		value, ok := actual[pkg]
		if !ok {
			return coverageError(fmt.Sprintf("coverage baseline package %s has no statements in current profile", pkg))
		}
		if floor < 0 || floor > 100 {
			return coverageError(fmt.Sprintf("invalid coverage floor for %s", pkg))
		}
		fmt.Printf("  %s: %.1f%% (floor %.1f%%)\n", pkg, value, floor)
		if value+1e-9 < floor {
			return coverageError(fmt.Sprintf("package %s coverage %.2f%% is below %.1f%% floor", pkg, value, floor))
		}
	}
	for pkg := range actual {
		if _, ok := floors[pkg]; !ok {
			return coverageError(fmt.Sprintf("package %s has no per-package coverage baseline", pkg))
		}
	}
	return nil
}
