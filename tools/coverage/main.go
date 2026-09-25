// Command coverage verifies the maintained library coverage budget. Examples,
// test helpers, and maintainer tools are exercised but excluded from the budget.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	minimum := flag.Float64("minimum", 90, "minimum maintained library statement coverage")
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
		if !strings.Contains(name, "/internal/testkit/") && !strings.HasSuffix(name, "/internal/testkit") {
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
	fmt.Println("Includes pkg and internal; excludes internal/testkit, examples, test, and tools.")
	if percent < *minimum {
		fail(fmt.Errorf("library coverage target not met"))
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func totals(profile []byte) (covered, total int, err error) {
	scan := bufio.NewScanner(bytes.NewReader(profile))
	if !scan.Scan() || !strings.HasPrefix(scan.Text(), "mode: ") {
		return 0, 0, fmt.Errorf("missing coverage mode")
	}
	// Merge duplicate blocks defensively; repeated records must not inflate coverage.
	type block struct {
		statements int
		hit        bool
	}
	blocks := map[string]block{}
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) != 3 {
			return 0, 0, fmt.Errorf("invalid coverage record")
		}
		statements, e := strconv.Atoi(fields[1])
		if e != nil || statements < 0 {
			return 0, 0, fmt.Errorf("invalid statement count")
		}
		count, e := strconv.ParseUint(fields[2], 10, 64)
		if e != nil {
			return 0, 0, fmt.Errorf("invalid execution count")
		}
		previous, exists := blocks[fields[0]]
		if exists && previous.statements != statements {
			return 0, 0, fmt.Errorf("inconsistent coverage block")
		}
		blocks[fields[0]] = block{statements, previous.hit || count > 0}
	}
	if scan.Err() != nil {
		return 0, 0, scan.Err()
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
