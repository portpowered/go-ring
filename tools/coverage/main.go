// Command coverage verifies the maintained library coverage budget. Examples,
// test helpers, and maintainer tools are exercised but excluded from the budget.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	minimum := flag.Float64("minimum", 90, "minimum maintained library statement coverage")
	baselinePath := flag.String("baselines", "tools/coverage/baselines.json", "per-package minimum coverage floors")
	flag.Parse()
	if *minimum < 0 || *minimum > 100 {
		fail(fmt.Errorf("minimum must be between zero and 100"))
	}
	os.Setenv("GOWORK", "off")
	output, err := exec.Command("go", "list", "./pkg/...", "./internal/...").Output()
	if err != nil {
		fail(err)
	}
	var packages []string
	for _, name := range strings.Fields(string(output)) {
		if !strings.Contains(name, "/internal/testkit/") && !strings.HasSuffix(name, "/internal/testkit") &&
			!strings.HasSuffix(name, "/pkg/generatedhttp") && !strings.HasSuffix(name, "/pkg/generatedsignaling") {
			packages = append(packages, name)
		}
	}
	if len(packages) == 0 {
		fail(fmt.Errorf("no maintained library packages found"))
	}
	profileFile, err := os.CreateTemp(".", "coverage.*.out")
	if err != nil {
		fail(err)
	}
	profilePath := profileFile.Name()
	if err = profileFile.Close(); err != nil {
		fail(err)
	}
	defer os.Remove(profilePath)
	cmd := exec.Command("go", "test", "-race", "-coverpkg="+strings.Join(packages, ","), "-coverprofile="+profilePath, "-covermode=atomic", "./...", "-timeout", "120s")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		fail(err)
	}
	profile, err := os.ReadFile(profilePath)
	if err != nil {
		fail(err)
	}
	profile = excludeGeneratedModels(profile)
	if err = os.WriteFile(profilePath, profile, 0o600); err != nil {
		fail(err)
	}
	covered, total, err := totals(profile)
	if err != nil {
		fail(err)
	}
	// Publish only the completed profile; parallel runs cannot corrupt each
	// other's instrumentation or coverage calculation.
	if err = os.Rename(profilePath, "coverage.out"); err != nil {
		fail(err)
	}
	percent := 100 * float64(covered) / float64(total)
	fmt.Printf("Maintained library coverage: %.2f%% (%d/%d statements); required %.2f%%\n", percent, covered, total, *minimum)
	fmt.Println("Includes maintained pkg and internal; excludes generated models/wire code, testkit, examples, tests, and tools.")
	if percent < *minimum {
		fail(fmt.Errorf("library coverage target not met"))
	}
	packageCoverage, err := packageTotals(profile)
	if err != nil {
		fail(err)
	}
	if err = checkPackageFloors(packageCoverage, *baselinePath); err != nil {
		fail(err)
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
		return 0, 0, fmt.Errorf("coverage profile contains no statements")
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
		return nil, fmt.Errorf("missing coverage mode")
	}
	// Merge duplicate blocks defensively; repeated records must not inflate coverage.
	blocks := map[string]coverageBlock{}
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid coverage record")
		}
		statements, e := strconv.Atoi(fields[1])
		if e != nil || statements < 0 {
			return nil, fmt.Errorf("invalid statement count")
		}
		count, e := strconv.ParseUint(fields[2], 10, 64)
		if e != nil {
			return nil, fmt.Errorf("invalid execution count")
		}
		previous, exists := blocks[fields[0]]
		if exists && previous.statements != statements {
			return nil, fmt.Errorf("inconsistent coverage block")
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
			return nil, fmt.Errorf("coverage path has no package: %s", path)
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
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read per-package coverage floors: %w", err)
	}
	floors := map[string]float64{}
	if err = json.Unmarshal(data, &floors); err != nil {
		return fmt.Errorf("parse per-package coverage floors: %w", err)
	}
	for pkg, floor := range floors {
		value, ok := actual[pkg]
		if !ok {
			return fmt.Errorf("coverage baseline package %s has no statements in current profile", pkg)
		}
		if floor < 0 || floor > 100 {
			return fmt.Errorf("invalid coverage floor for %s", pkg)
		}
		fmt.Printf("  %s: %.1f%% (floor %.1f%%)\n", pkg, value, floor)
		if value+1e-9 < floor {
			return fmt.Errorf("package %s coverage %.2f%% is below %.1f%% floor", pkg, value, floor)
		}
	}
	for pkg := range actual {
		if _, ok := floors[pkg]; !ok {
			return fmt.Errorf("package %s has no per-package coverage baseline", pkg)
		}
	}
	return nil
}
