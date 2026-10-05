package main

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const exampleEvidenceKey = "x-example-evidence"

const windowsOS = "windows"

const fileScheme = "file"

type document struct {
	path     string
	uri      string
	data     any
	resource any
}

type location struct {
	doc     *document
	pointer string
}

type report struct {
	documents int
	examples  int
	groups    int
}

type validationIssuesError []string

func (issues validationIssuesError) Error() string {
	return strings.Join(issues, "\n")
}

type validator struct {
	docs               map[string]*document
	byURI              map[string]*document
	groupScopes        map[string]string
	groupComponents    map[string]map[string]any
	issues             []string
	usedExampleObjects map[string]bool
	compiled           map[string]*jsonschema.Schema
	compiler           *jsonschema.Compiler
	report             report
}

func validateRoots(roots []string) (report, error) {
	schemaValidator := &validator{
		docs:               make(map[string]*document),
		byURI:              make(map[string]*document),
		groupScopes:        make(map[string]string),
		groupComponents:    make(map[string]map[string]any),
		issues:             nil,
		usedExampleObjects: make(map[string]bool),
		compiled:           make(map[string]*jsonschema.Schema),
		compiler:           nil,
		report:             report{documents: 0, examples: 0, groups: 0},
	}

	err := schemaValidator.loadRoots(roots)
	if err != nil {
		return schemaValidator.report, err
	}

	schemaValidator.prepareCanonicalGroups()
	schemaValidator.loadReferencedDocuments()
	schemaValidator.buildCompiler()
	schemaValidator.validateSchemaDeclarations()
	schemaValidator.validateOpenAPIExamplesAndGroups()
	schemaValidator.validateAsyncAPIExamplesAndGroups()
	schemaValidator.rejectUnownedExampleComponents()

	schemaValidator.report.documents = len(schemaValidator.docs)
	if len(schemaValidator.issues) > 0 {
		sort.Strings(schemaValidator.issues)

		return schemaValidator.report, validationIssuesError(schemaValidator.issues)
	}

	return schemaValidator.report, nil
}

func (v *validator) loadRoots(roots []string) error {
	var paths []string

	for _, root := range roots {
		rootPaths, err := schemaPaths(root)
		if err != nil {
			return err
		}

		paths = append(paths, rootPaths...)
	}

	sort.Strings(paths)

	for _, path := range paths {
		_, err := v.loadDocument(path)
		if err != nil {
			v.addIssue(path, "#", "cannot parse schema document")
		}
	}

	if len(v.docs) == 0 && len(v.issues) == 0 {
		return errNoSchemaDocuments
	}

	return nil
}

func schemaPaths(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, wrapError("resolve schema root", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, wrapError("read schema root", err)
	}

	if !info.IsDir() {
		if isYAMLOrJSON(abs) {
			return []string{abs}, nil
		}

		return nil, nil
	}

	var paths []string

	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || !isYAMLOrJSON(path) || isGeneratedOpenAPIBundle(path) {
			return nil
		}

		paths = append(paths, path)

		return nil
	})
	if err != nil {
		return nil, wrapError("scan schema root "+root, err)
	}

	return paths, nil
}

func isGeneratedOpenAPIBundle(path string) bool {
	if !strings.EqualFold(filepath.Base(path), "openapi.yaml") {
		return false
	}

	dir := filepath.Dir(path)
	for _, marker := range []string{
		filepath.Join(dir, "openapi.base.yaml"),
		filepath.Join(dir, "model-groups.json"),
		filepath.Join(dir, "openapi", "sources.yaml"),
	} {
		_, err := os.Stat(marker)
		if err == nil {
			return true
		}
	}

	return false
}

func isYAMLOrJSON(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func canonicalPath(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == windowsOS {
		return strings.ToLower(clean)
	}

	return clean
}

func fileURI(path string) string {
	abs, _ := filepath.Abs(path)

	uriPath := filepath.ToSlash(abs)
	if runtime.GOOS == windowsOS && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}

	var uri url.URL

	uri.Scheme = fileScheme
	uri.Path = uriPath

	return uri.String()
}

func (v *validator) loadDocument(path string) (*document, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, wrapError("resolve schema document path", err)
	}

	key := canonicalPath(abs)
	if doc, ok := v.docs[key]; ok {
		return doc, nil
	}

	contents, err := fs.ReadFile(os.DirFS(filepath.Dir(abs)), filepath.Base(abs))
	if err != nil {
		return nil, wrapError("read schema document", err)
	}

	var parsed any
	if strings.EqualFold(filepath.Ext(abs), ".json") {
		err = json.Unmarshal(contents, &parsed)
	} else {
		err = yaml.Unmarshal(contents, &parsed)
	}

	if err != nil {
		return nil, wrapError("parse schema document", err)
	}

	jsonBytes, err := json.Marshal(parsed)
	if err != nil {
		return nil, wrapError("normalize schema document", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(jsonBytes)))
	decoder.UseNumber()

	err = decoder.Decode(&parsed)
	if err != nil {
		return nil, wrapError("decode normalized schema document", err)
	}

	doc := &document{path: abs, uri: fileURI(abs), data: parsed, resource: nil}
	v.docs[key] = doc
	v.byURI[doc.uri] = doc

	return doc, nil
}

func (v *validator) prepareCanonicalGroups() {
	for _, manifest := range v.sortedDocuments() {
		if !strings.EqualFold(filepath.Base(manifest.path), "model-groups.json") {
			continue
		}

		root, ok := asMap(manifest.data)
		if !ok {
			continue
		}

		apiRoot := filepath.Dir(manifest.path)
		repoRoot := filepath.Dir(apiRoot)
		scope := canonicalPath(apiRoot)
		sources := canonicalGroupSources(root)
		groupDocs := v.loadCanonicalGroupDocuments(manifest, repoRoot, scope, sources)
		v.groupComponents[scope] = mergeCanonicalGroupComponents(v, groupDocs)
	}
}

func canonicalGroupSources(root map[string]any) []string {
	var sources []string
	if input, ok := root["input"].(string); ok {
		sources = append(sources, input)
	}

	groups, groupsAreArray := root["groups"].([]any)
	if !groupsAreArray {
		return sources
	}

	for _, value := range groups {
		group, groupIsObject := asMap(value)
		if !groupIsObject {
			continue
		}

		source, sourceIsString := group["source"].(string)
		if sourceIsString {
			sources = append(sources, source)
		}
	}

	return sources
}

func (v *validator) loadCanonicalGroupDocuments(
	manifest *document,
	repoRoot string,
	scope string,
	sources []string,
) []*document {
	groupDocs := make([]*document, 0, len(sources))
	seen := make(map[string]bool)

	for _, source := range sources {
		path := source
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, filepath.FromSlash(source))
		}

		doc, err := v.loadDocument(path)
		if err != nil {
			v.addIssue(manifest.path, "#", "canonical schema source is missing")

			continue
		}

		key := canonicalPath(doc.path)
		if seen[key] {
			continue
		}

		seen[key] = true
		v.groupScopes[key] = scope

		groupDocs = append(groupDocs, doc)
	}

	return groupDocs
}

func mergeCanonicalGroupComponents(schemaValidator *validator, groupDocs []*document) map[string]any {
	components := make(map[string]any)

	for _, doc := range groupDocs {
		documentRoot, documentRootIsObject := asMap(doc.data)
		if !documentRootIsObject {
			continue
		}

		docComponents, docComponentsAreMap := asMap(documentRoot["components"])
		if !docComponentsAreMap {
			continue
		}

		mergeCanonicalComponentSections(schemaValidator, doc, components, docComponents)
	}

	return components
}

func mergeCanonicalComponentSections(
	schemaValidator *validator,
	doc *document,
	components map[string]any,
	docComponents map[string]any,
) {
	for section, rawValues := range docComponents {
		values, valuesAreMap := asMap(rawValues)
		if !valuesAreMap {
			continue
		}

		merged, ok := asMap(components[section])
		if !ok {
			merged = make(map[string]any)
			components[section] = merged
		}

		for name, value := range values {
			existing, exists := merged[name]
			if exists && !jsonValuesEqual(existing, value) {
				componentPointer := joinPointer(joinPointer(joinPointer("#", "components"), section), name)
				schemaValidator.addIssue(doc.path, componentPointer, "ambiguous canonical component owner")

				continue
			}

			merged[name] = value
		}
	}
}

func jsonValuesEqual(first, second any) bool {
	firstJSON, firstErr := json.Marshal(first)
	secondJSON, secondErr := json.Marshal(second)

	return firstErr == nil && secondErr == nil && string(firstJSON) == string(secondJSON)
}
