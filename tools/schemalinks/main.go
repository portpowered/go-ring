// Package main checks schema links against a rendered documentation site.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	checkInternal      = "internal"
	checkExternal      = "external"
	checkExternalPages = "external-pages"
	checkTemplate      = "template"
	manifestDirMode    = 0o750
	linkSummaryFormat  = "Checked %d internal schema links; recorded %d external or templated links in %s\n"
)

type linkFailureError string

func (failure linkFailureError) Error() string {
	return string(failure)
}

type contextualError struct {
	message string
	cause   error
}

func (failure contextualError) Error() string {
	return failure.message + ": " + failure.cause.Error()
}

func (failure contextualError) Unwrap() error {
	return failure.cause
}

func withErrorContext(cause error, format string, values ...any) error {
	return contextualError{message: fmt.Sprintf(format, values...), cause: cause}
}

type linkRecord struct {
	schemaLink

	Check    string        `json:"check"`
	Evidence *linkEvidence `json:"evidence,omitempty"`
}

type manifest struct {
	Schemas  []string     `json:"schemas"`
	SiteRoot string       `json:"site"`
	BasePath string       `json:"base"`
	Links    []linkRecord `json:"links"`
}

type reviewEntry struct {
	Publisher   string                     `json:"publisher"`
	Title       string                     `json:"title"`
	ReviewedOn  string                     `json:"reviewed"`
	Source      string                     `json:"source"`
	Assessment  string                     `json:"assessment"`
	Limitations string                     `json:"limitations"`
	Operations  map[string]operationReview `json:"operations"`
}

type operationReview struct {
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type linkEvidence struct {
	Publisher   string              `json:"publisher"`
	Title       string              `json:"title"`
	ReviewedOn  string              `json:"reviewed"`
	Source      string              `json:"source"`
	Assessment  string              `json:"assessment"`
	Operations  []operationEvidence `json:"operations"`
	Limitations string              `json:"limitations"`
}

type operationEvidence struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type runConfig struct {
	displaySiteDir string
	siteRoot       string
	basePath       string
	manifestPath   string
	reviewPath     string
}

func main() {
	siteDir := flag.String("site", "site", "rendered documentation site directory")
	schemaList := flag.String("schemas", "api/openapi.yaml", "comma-separated schema entrypoints")
	basePath := flag.String("base-path", "", "GitHub Pages base path, defaulting to the repository name")
	manifestPath := flag.String("manifest", "", "manifest output path, defaulting to <site>/schema-links.json")
	reviewPath := flag.String("reviews", "tools/schemalinks/reviews.json", "reviewed externalDocs evidence file")

	flag.Parse()

	err := run(*siteDir, *schemaList, *basePath, *manifestPath, *reviewPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(siteDir, schemaList, basePath, manifestPath, reviewPath string) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return withErrorContext(err, "get repository directory")
	}

	return runFrom(repoRoot, siteDir, schemaList, basePath, manifestPath, reviewPath)
}

func runFrom(repoRoot, siteDir, schemaList, basePath, manifestPath, reviewPath string) error {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return withErrorContext(err, "resolve repository directory")
	}

	config, err := newRunConfig(repoRoot, siteDir, basePath, manifestPath, reviewPath)
	if err != nil {
		return err
	}

	schemaInputs := resolveSchemaInputs(repoRoot, schemaList)

	links, err := collectSchemaLinks(repoRoot, schemaInputs)
	if err != nil {
		return err
	}

	reviews, err := loadReviews(config.reviewPath)
	if err != nil {
		return err
	}

	return writeRunResult(repoRoot, config, schemaInputs, links, reviews)
}

func newRunConfig(repoRoot, siteDir, basePath, manifestPath, reviewPath string) (runConfig, error) {
	config := runConfig{
		displaySiteDir: siteDir,
		siteRoot:       "",
		basePath:       basePath,
		manifestPath:   "",
		reviewPath:     "",
	}

	siteRoot, err := filepath.Abs(rootedPath(repoRoot, siteDir))
	if err != nil {
		return runConfig{}, withErrorContext(err, "resolve rendered site directory")
	}

	config.siteRoot = siteRoot
	if !isFile(filepath.Join(config.siteRoot, "index.html")) {
		return runConfig{}, linkFailureError("missing rendered site: " + filepath.Join(config.siteRoot, "index.html"))
	}

	if config.basePath == "" {
		config.basePath = "/" + filepath.Base(repoRoot)
	}

	config.basePath = normalizeBasePath(config.basePath)
	if manifestPath == "" {
		config.manifestPath = filepath.Join(config.siteRoot, "schema-links.json")
	} else {
		config.manifestPath = rootedPath(repoRoot, manifestPath)
	}

	config.reviewPath = rootedPath(repoRoot, reviewPath)

	return config, nil
}

func writeRunResult(
	repoRoot string,
	config runConfig,
	schemaInputs []string,
	links []schemaLink,
	reviews map[string]reviewEntry,
) error {
	result := newManifest(config.displaySiteDir, config.basePath, repoRoot, schemaInputs)
	records, broken, checkedInternal, manualExternal := evaluateLinks(links, config.siteRoot, config.basePath, reviews)
	result.Links = records

	writeErr := writeManifest(config.manifestPath, result)
	if writeErr != nil {
		return writeErr
	}

	if len(broken) > 0 {
		message := fmt.Sprintf(
			"broken internal schema links (manifest: %s):\n  %s",
			filepath.ToSlash(config.manifestPath), strings.Join(broken, "\n  "),
		)

		return linkFailureError(message)
	}

	_, err := fmt.Fprintf(
		os.Stdout,
		linkSummaryFormat,
		checkedInternal,
		manualExternal,
		filepath.ToSlash(config.manifestPath),
	)
	if err != nil {
		return withErrorContext(err, "write link check summary")
	}

	return nil
}

func rootedPath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(root, path)
}

func resolveSchemaInputs(repoRoot, schemaList string) []string {
	parts := strings.Split(schemaList, ",")

	inputs := make([]string, 0, len(parts))

	for _, input := range parts {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if !filepath.IsAbs(input) {
			input = filepath.Join(repoRoot, input)
		}

		inputs = append(inputs, input)
	}

	sort.Strings(inputs)

	return inputs
}

func newManifest(siteDir, basePath, repoRoot string, schemaInputs []string) manifest {
	result := manifest{
		Schemas:  make([]string, 0, len(schemaInputs)),
		SiteRoot: filepath.ToSlash(siteDir),
		BasePath: basePath,
		Links:    make([]linkRecord, 0),
	}
	for _, input := range schemaInputs {
		result.Schemas = append(result.Schemas, relativeTo(repoRoot, input))
	}

	return result
}

func evaluateLinks(
	links []schemaLink,
	siteRoot string,
	basePath string,
	reviews map[string]reviewEntry,
) ([]linkRecord, []string, int, int) {
	records := make([]linkRecord, 0, len(links))

	var broken []string

	checkedInternal := 0
	manualExternal := 0

	for _, link := range links {
		record, failure := evaluateLink(link, siteRoot, basePath, reviews)

		records = append(records, record)

		if record.Check == checkInternal {
			checkedInternal++
		}

		if isManualCheck(record.Check) {
			manualExternal++
		}

		if failure != "" {
			broken = append(broken, failure)
		}
	}

	return records, broken, checkedInternal, manualExternal
}

func evaluateLink(link schemaLink, siteRoot, basePath string, reviews map[string]reviewEntry) (linkRecord, string) {
	check, destinationError := checkDestination(link, siteRoot, basePath)

	record := linkRecord{schemaLink: link, Check: check, Evidence: nil}

	if link.Kind == linkKindExternalDocs && check != checkInternal {
		record.Evidence = findEvidence(reviews, link)
		if record.Evidence == nil {
			operation := link.Operation
			if operation == "" {
				operation = "(no owning operation resolved)"
			}

			message := fmt.Sprintf(
				"%s:%d %s %s: externalDocs has no reviewed provider evidence for %s",
				link.Source, link.Line, link.Pointer, link.URL, operation,
			)

			return record, message
		}
	}

	if destinationError != nil {
		message := fmt.Sprintf("%s:%d %s %s: %v", link.Source, link.Line, link.Pointer, link.URL, destinationError)

		return record, message
	}

	return record, ""
}

func isManualCheck(check string) bool {
	switch check {
	case checkExternal, checkExternalPages, checkTemplate:
		return true
	default:
		return false
	}
}

func loadReviews(path string) (map[string]reviewEntry, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- reviews are an explicit CLI input parsed as JSON data.
	if errors.Is(err, os.ErrNotExist) {
		return map[string]reviewEntry{}, nil
	}

	if err != nil {
		return nil, withErrorContext(err, "read external link reviews %s", path)
	}

	var reviews map[string]reviewEntry

	decodeErr := json.Unmarshal(contents, &reviews)
	if decodeErr != nil {
		return nil, withErrorContext(decodeErr, "parse external link reviews %s", path)
	}

	return reviews, nil
}

func findEvidence(reviews map[string]reviewEntry, link schemaLink) *linkEvidence {
	review, ok := reviews[link.URL]
	if !ok {
		return nil
	}

	operationNames := append([]string(nil), link.Operations...)
	if len(operationNames) == 0 && link.Operation != "" {
		operationNames = append(operationNames, link.Operation)
	}

	sort.Strings(operationNames)

	if len(operationNames) == 0 {
		return nil
	}

	operations := make([]operationEvidence, 0, len(operationNames))

	for _, name := range operationNames {
		operation, ok := review.Operations[name]
		if !ok || !hasReviewEvidence(review, operation) {
			return nil
		}

		operations = append(operations, operationEvidence{
			Name:     name,
			Status:   operation.Status,
			Evidence: operation.Evidence,
		})
	}

	return &linkEvidence{
		Publisher:   review.Publisher,
		Title:       review.Title,
		ReviewedOn:  review.ReviewedOn,
		Source:      review.Source,
		Assessment:  review.Assessment,
		Operations:  operations,
		Limitations: review.Limitations,
	}
}

func hasReviewEvidence(review reviewEntry, operation operationReview) bool {
	return review.Publisher != "" && review.Title != "" && review.ReviewedOn != "" &&
		review.Source != "" && review.Assessment != "" && review.Limitations != "" &&
		operation.Status != "" && operation.Evidence != ""
}

func checkDestination(link schemaLink, siteRoot, basePath string) (string, error) {
	if isOpenAPIVariableTemplate(link) {
		return checkTemplate, nil
	}

	if strings.ContainsAny(link.URL, "{}") {
		return checkExternal, linkFailureError("malformed URL template")
	}

	parsed, err := url.Parse(link.URL)
	if err != nil {
		return checkExternal, withErrorContext(err, "parse schema URL")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return checkExternal, nil
	}

	if !strings.EqualFold(parsed.Hostname(), "portpowered.github.io") {
		return checkExternal, nil
	}

	decodedPath, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return checkInternal, withErrorContext(err, "invalid path encoding")
	}

	if decodedPath != basePath && !strings.HasPrefix(decodedPath, basePath+"/") {
		return checkExternalPages, nil
	}

	return checkInternal, checkInternalDestination(decodedPath, siteRoot, basePath)
}

func checkInternalDestination(decodedPath, siteRoot, basePath string) error {
	relative := strings.TrimPrefix(strings.TrimPrefix(decodedPath, basePath), "/")

	cleaned := filepath.Clean(filepath.FromSlash(relative))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || filepath.IsAbs(cleaned) {
		return linkFailureError("path escapes rendered site")
	}

	target := filepath.Join(siteRoot, cleaned)

	candidates := []string{target}

	if strings.HasSuffix(decodedPath, "/") || filepath.Ext(target) == "" {
		candidates = append(candidates, filepath.Join(target, "index.html"))
	}

	if slices.ContainsFunc(candidates, isFile) {
		return nil
	}

	return linkFailureError("rendered target does not exist")
}

func isOpenAPIVariableTemplate(link schemaLink) bool {
	if link.Kind == linkKindExternalDocs {
		return false
	}

	if !strings.ContainsAny(link.URL, "{}") {
		return false
	}

	serverVariable, valid := replaceServerVariables(link.URL)
	if !valid {
		return false
	}

	parsed, err := url.Parse(serverVariable)

	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
}

func replaceServerVariables(raw string) (string, bool) {
	var output strings.Builder

	found := false

	for index := 0; index < len(raw); {
		if raw[index] == '}' {
			return "", false
		}

		if raw[index] != '{' {
			output.WriteByte(raw[index])

			index++

			continue
		}

		end := strings.IndexByte(raw[index+1:], '}')
		if end < 0 {
			return "", false
		}

		end += index + 1

		name := raw[index+1 : end]
		if !validServerVariableName(name) {
			return "", false
		}

		output.WriteString("variable")

		found = true
		index = end + 1
	}

	return output.String(), found
}

func validServerVariableName(name string) bool {
	if name == "" {
		return false
	}

	for _, character := range name {
		if !isServerVariableCharacter(character) {
			return false
		}
	}

	return true
}

func isServerVariableCharacter(character rune) bool {
	return isASCIILetter(character) || isASCIIDigit(character) || character == '_'
}

func isASCIILetter(character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func isASCIIDigit(character rune) bool {
	return character >= '0' && character <= '9'
}

func normalizeBasePath(value string) string {
	trimmed := strings.TrimRight(value, "/")
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}

	return trimmed
}

func isFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

func writeManifest(path string, value manifest) error {
	mkdirErr := os.MkdirAll(filepath.Dir(path), manifestDirMode)
	if mkdirErr != nil {
		return withErrorContext(mkdirErr, "create manifest directory %s", filepath.Dir(path))
	}

	file, err := os.Create(path) // #nosec G304 -- the explicit CLI output path selects the generated manifest destination.
	if err != nil {
		return withErrorContext(err, "create schema link manifest %s", path)
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()

	if encodeErr != nil {
		return withErrorContext(encodeErr, "encode schema link manifest %s", path)
	}

	if closeErr != nil {
		return withErrorContext(closeErr, "close schema link manifest %s", path)
	}

	return nil
}
