// Command compatibility checks the supported Go API against a Git baseline.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	modulePath               = "github.com/portpowered/go-ring"
	apiDiffTool              = "golang.org/x/exp/cmd/apidiff@v0.0.0-20260908205506-85c1c2202aba"
	previousRelease          = "previous-release"
	versionTagCaptureCount   = 4
	toolDirectoryPermissions = 0o755
	summaryFilePermissions   = 0o600
)

var publicPackages = []string{
	"pkg/ring",
	"pkg/ringapimodels",
}

var (
	stableTagPattern  = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	releaseTagPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-[0-9A-Za-z.-]+)?$`)
)

type gateError struct {
	message string
	cause   error
}

func (e gateError) Error() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + ": " + e.cause.Error()
}

func (e gateError) Unwrap() error {
	return e.cause
}

type version struct {
	major int
	minor int
	patch int
}

type incompatibleChange struct {
	packageName string
	details     string
}

type checkOptions struct {
	baseRef        string
	releaseVersion string
	policy         string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	baseRef := flag.String("base", "", "Git ref to compare against, or previous-release")
	releaseVersion := flag.String("version", "", "release tag, used with -policy=release")
	policy := flag.String("policy", "report", "report or release")
	flag.Parse()
	options := checkOptions{baseRef: *baseRef, releaseVersion: *releaseVersion, policy: *policy}
	if options.baseRef == "" {
		return gateError{message: "-base is required"}
	}
	if options.policy != "report" && options.policy != "release" {
		return gateError{message: "-policy must be report or release"}
	}
	if options.policy == "release" && options.releaseVersion == "" {
		return gateError{message: "-version is required with -policy=release"}
	}

	root, err := repositoryRoot()
	if err != nil {
		return gateError{message: "find repository root", cause: err}
	}
	requestedBase := options.baseRef
	if isZeroRef(requestedBase) {
		requestedBase = previousRelease
	}
	base, found, err := resolveBase(root, requestedBase, options.releaseVersion)
	if err != nil {
		return gateError{message: "resolve baseline", cause: err}
	}
	if !found {
		fmt.Println("No previous stable release tag found; skipping API compatibility comparison.")
		return nil
	}
	if options.policy == "release" {
		if err := validateReleaseOrder(base, options.releaseVersion); err != nil {
			return gateError{message: "validate release version", cause: err}
		}
	}

	changes, err := collectIncompatibleChanges(root, base)
	if err != nil {
		return err
	}
	return reportResults(options, base, changes)
}

func collectIncompatibleChanges(root, base string) ([]incompatibleChange, error) {
	tempDir, err := os.MkdirTemp("", "go-ring-api-compatibility-")
	if err != nil {
		return nil, gateError{message: "create temporary directory", cause: err}
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove compatibility temporary directory: %v\n", err)
		}
	}()

	baseDir := filepath.Join(tempDir, "base")
	if err := git(root, "worktree", "add", "--detach", baseDir, base); err != nil {
		return nil, gateError{message: "create baseline worktree", cause: err}
	}
	defer func() {
		if err := git(root, "worktree", "remove", "--force", baseDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove baseline worktree: %v\n", err)
		}
	}()

	toolDir := filepath.Join(tempDir, "bin")
	if err := os.Mkdir(toolDir, toolDirectoryPermissions); err != nil {
		return nil, gateError{message: "create tool directory", cause: err}
	}
	if err := installAPIDiff(root, toolDir); err != nil {
		return nil, gateError{message: "install API comparison tool", cause: err}
	}
	tool := filepath.Join(toolDir, "apidiff")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}

	changes := make([]incompatibleChange, 0, len(publicPackages))
	for _, packageName := range publicPackages {
		packagePath := modulePath + "/" + packageName
		baseFileName := strings.ReplaceAll(packageName, "/", "-") + "-base.export"
		currentFileName := strings.ReplaceAll(packageName, "/", "-") + "-current.export"
		oldData := filepath.Join(tempDir, baseFileName)
		newData := filepath.Join(tempDir, currentFileName)
		if err := writeExportData(tool, baseDir, packagePath, oldData); err != nil {
			return nil, gateError{message: "read baseline API for " + packagePath, cause: err}
		}
		if err := writeExportData(tool, root, packagePath, newData); err != nil {
			return nil, gateError{message: "read current API for " + packagePath, cause: err}
		}
		packageChanges, err := compareAPIs(tool, oldData, newData)
		if err != nil {
			return nil, gateError{message: "compare API for " + packagePath, cause: err}
		}
		if packageChanges != "" {
			changes = append(changes, incompatibleChange{packageName: packageName, details: packageChanges})
		}
	}
	return changes, nil
}

func reportResults(options checkOptions, base string, changes []incompatibleChange) error {
	var releaseMessage string
	if len(changes) > 0 && options.policy == "release" {
		allowed, reason, err := releaseAllowsBreak(base, options.releaseVersion)
		if err != nil {
			return gateError{message: "validate release version", cause: err}
		}
		if !allowed {
			if err := writeReportSummary(options.policy, base, changes, "", true); err != nil {
				return gateError{message: "write CI summary", cause: err}
			}
			return gateError{message: fmt.Sprintf("release %s has incompatible API changes from %s; the version increase does not permit these changes", options.releaseVersion, base)}
		}
		releaseMessage = reason
	}

	if err := writeReportSummary(options.policy, base, changes, releaseMessage, false); err != nil {
		return gateError{message: "write CI summary", cause: err}
	}
	if len(changes) == 0 {
		fmt.Printf("Public Go API is compatible with %s.\n", base)
		return nil
	}
	for _, change := range changes {
		fmt.Printf("Incompatible changes in %s:\n%s\n", change.packageName, change.details)
	}
	if options.policy == "report" {
		fmt.Println("Report only: a reviewer must explicitly acknowledge this report before approving an intentional API break.")
		return nil
	}
	fmt.Printf("Release %s permits these incompatible changes: %s.\n", options.releaseVersion, releaseMessage)
	return nil
}

func repositoryRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = workingDir
	cmd.Env = withEnvironment(os.Environ(), "GOWORK", "off")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	moduleFile := strings.TrimSpace(string(output))
	if moduleFile == "" || moduleFile == os.DevNull {
		return "", gateError{message: "current directory is not in a Go module"}
	}
	return filepath.Dir(moduleFile), nil
}

func resolveBase(root, requested, releaseVersion string) (string, bool, error) {
	if requested != previousRelease {
		return requested, true, nil
	}
	tags, err := runGit(root, "tag", "--list", "v*", "--sort=-version:refname")
	if err != nil {
		return "", false, err
	}
	var target version
	if releaseVersion != "" {
		target, err = parseVersion(releaseVersion, releaseTagPattern)
		if err != nil {
			return "", false, gateError{message: "release version must be a vMAJOR.MINOR.PATCH tag, optionally with a prerelease suffix", cause: err}
		}
	}

	hasOtherStableTag := false
	for _, tag := range strings.Fields(string(tags)) {
		candidate, parseErr := parseVersion(tag, stableTagPattern)
		if parseErr != nil {
			continue
		}
		if releaseVersion == "" {
			return tag, true, nil
		}
		if tag != releaseVersion {
			hasOtherStableTag = true
		}
		if compareVersions(candidate, target) < 0 {
			return tag, true, nil
		}
	}
	if !hasOtherStableTag {
		return "", false, nil
	}
	return "", false, gateError{message: "no stable release tag is older than " + releaseVersion}
}

func releaseAllowsBreak(baseTag, releaseTag string) (bool, string, error) {
	base, err := parseVersion(baseTag, stableTagPattern)
	if err != nil {
		return false, "", err
	}
	release, err := parseVersion(releaseTag, releaseTagPattern)
	if err != nil {
		return false, "", err
	}
	if release.major > base.major {
		return true, "the major version increased", nil
	}
	if base.major == 0 && release.major == 0 && release.minor > base.minor {
		return true, "the v0 minor version increased", nil
	}
	return false, "", nil
}

func validateReleaseOrder(baseTag, releaseTag string) error {
	base, err := parseVersion(baseTag, stableTagPattern)
	if err != nil {
		return err
	}
	release, err := parseVersion(releaseTag, releaseTagPattern)
	if err != nil {
		return err
	}
	if compareVersions(release, base) <= 0 {
		return gateError{message: fmt.Sprintf("release tag %s must be newer than baseline %s", releaseTag, baseTag)}
	}
	return nil
}

func parseVersion(tag string, pattern *regexp.Regexp) (version, error) {
	match := pattern.FindStringSubmatch(tag)
	if len(match) != versionTagCaptureCount {
		return version{}, gateError{message: "invalid version tag " + tag}
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return version{}, gateError{message: "parse major version in " + tag, cause: err}
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return version{}, gateError{message: "parse minor version in " + tag, cause: err}
	}
	patch, err := strconv.Atoi(match[3])
	if err != nil {
		return version{}, gateError{message: "parse patch version in " + tag, cause: err}
	}
	return version{major: major, minor: minor, patch: patch}, nil
}

func compareVersions(left, right version) int {
	if left.major != right.major {
		if left.major < right.major {
			return -1
		}
		return 1
	}
	if left.minor != right.minor {
		if left.minor < right.minor {
			return -1
		}
		return 1
	}
	if left.patch < right.patch {
		return -1
	}
	if left.patch > right.patch {
		return 1
	}
	return 0
}

func installAPIDiff(root, toolDir string) error {
	cmd := exec.Command("go", "install", apiDiffTool)
	cmd.Dir = root
	cmd.Env = withEnvironment(os.Environ(), "GOBIN", toolDir)
	cmd.Env = withEnvironment(cmd.Env, "GOTOOLCHAIN", "auto")
	cmd.Env = withEnvironment(cmd.Env, "GOWORK", "off")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writeExportData(tool, directory, packagePath, outputPath string) error {
	cmd := exec.Command(tool, "-w", outputPath, packagePath)
	cmd.Dir = directory
	cmd.Env = withEnvironment(os.Environ(), "GOTOOLCHAIN", "auto")
	cmd.Env = withEnvironment(cmd.Env, "GOWORK", "off")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func compareAPIs(tool, oldData, newData string) (string, error) {
	cmd := exec.Command(tool, "-incompatible", oldData, newData)
	cmd.Env = withEnvironment(os.Environ(), "GOTOOLCHAIN", "auto")
	cmd.Env = withEnvironment(cmd.Env, "GOWORK", "off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", gateError{message: strings.TrimSpace(string(output)), cause: err}
	}
	return strings.TrimSpace(string(output)), nil
}

func writeReportSummary(policy, base string, changes []incompatibleChange, releaseMessage string, releaseFailed bool) error {
	var summary strings.Builder
	summary.WriteString("## Public Go API compatibility\n\n")
	summary.WriteString("Baseline: `" + base + "`\n\n")
	if len(changes) == 0 {
		summary.WriteString("No incompatible API changes were found.\n")
	} else {
		summary.WriteString("Incompatible API changes were found.\n\n")
		if policy == "report" {
			summary.WriteString("This CI check reports changes without blocking merges. Reviewers must explicitly acknowledge this report before approving an intentional API break.\n\n")
		} else {
			summary.WriteString("The release check evaluates these changes against the release version policy. Reviewers should confirm that any permitted break was explicitly reviewed in its pull request.\n\n")
		}
		for _, change := range changes {
			summary.WriteString("### `" + change.packageName + "`\n\n```text\n")
			summary.WriteString(change.details + "\n```\n\n")
		}
	}
	if policy == "release" {
		switch {
		case releaseFailed:
			summary.WriteString("Release compatibility check failed because the version does not permit the incompatible changes.\n")
		case releaseMessage == "" && len(changes) == 0:
			summary.WriteString("Release compatibility check passed.\n")
		case releaseMessage != "":
			summary.WriteString("Release compatibility check passed: " + releaseMessage + ".\n")
		}
	}
	text := summary.String()
	fmt.Print(text)
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	// #nosec G304 -- GitHub Actions supplies this path for the current step summary.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, summaryFilePermissions)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(text + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func isZeroRef(ref string) bool {
	return ref != "" && strings.Trim(ref, "0") == ""
}

func git(root string, args ...string) error {
	_, err := runGit(root, args...)
	return err
}

func runGit(root string, args ...string) ([]byte, error) {
	gitArgs := append([]string{"-c", "safe.directory=" + filepath.ToSlash(root), "-C", root}, args...)
	// #nosec G204 -- Git is invoked with fixed subcommands and separate argument values.
	cmd := exec.Command("git", gitArgs...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

func withEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}
