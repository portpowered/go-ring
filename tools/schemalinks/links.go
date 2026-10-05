package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	linkKindDescription   = "description"
	linkKindExternalDocs  = "externalDocs"
	linkKindURL           = "url"
	graphqlExtension      = ".graphql"
	graphqlShortExtension = ".gql"
)

type schemaLink struct {
	URL            string   `json:"url"`
	Kind           string   `json:"kind"`
	Source         string   `json:"source"`
	Pointer        string   `json:"pointer"`
	Operation      string   `json:"operation,omitempty"`
	Operations     []string `json:"operations,omitempty"`
	Line           int      `json:"line"`
	ReferencedFrom []string `json:"references,omitempty"`
}

type yamlFile struct {
	documents []*yaml.Node
}

type linkCollector struct {
	repoRoot   string
	cache      map[string]yamlFile
	links      map[string]*schemaLink
	errors     []error
	urlPattern *regexp.Regexp
}

func collectSchemaLinks(repoRoot string, schemaInputs []string) ([]schemaLink, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, withErrorContext(err, "resolve repository root")
	}

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, withErrorContext(err, "resolve repository root symlinks")
	}

	inputs, err := normalizeSchemaInputs(root, schemaInputs)
	if err != nil {
		return nil, err
	}

	collector := newLinkCollector(root)

	files, err := schemaFiles(inputs)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		collectErr := collector.collectFile(file)
		if collectErr != nil {
			return nil, collectErr
		}
	}

	if len(collector.errors) > 0 {
		return nil, errors.Join(collector.errors...)
	}

	return collector.sortedLinks(), nil
}

func newLinkCollector(root string) *linkCollector {
	return &linkCollector{
		repoRoot:   root,
		cache:      make(map[string]yamlFile),
		links:      make(map[string]*schemaLink),
		errors:     nil,
		urlPattern: regexp.MustCompile(`https?://[^\s'"<>\)\]\x60]+`),
	}
}

func (c *linkCollector) collectFile(file string) error {
	if isGraphQLFile(file) {
		return c.scanGraphQL(file)
	}

	documents, err := c.load(file)
	if err != nil {
		return withErrorContext(err, "parse %s", relativeTo(c.repoRoot, file))
	}

	for _, document := range documents {
		if len(document.Content) == 0 {
			continue
		}

		c.walk(file, document.Content[0], "#", nil, make(map[string]bool))
	}

	return nil
}

func (c *linkCollector) sortedLinks() []schemaLink {
	links := make([]schemaLink, 0, len(c.links))
	for _, link := range c.links {
		link.ReferencedFrom = sortedUnique(link.ReferencedFrom)
		link.Operations = sortedUnique(link.Operations)
		links = append(links, *link)
	}

	sort.Slice(links, func(leftIndex, rightIndex int) bool {
		if links[leftIndex].Source != links[rightIndex].Source {
			return links[leftIndex].Source < links[rightIndex].Source
		}

		if links[leftIndex].Pointer != links[rightIndex].Pointer {
			return links[leftIndex].Pointer < links[rightIndex].Pointer
		}

		return links[leftIndex].URL < links[rightIndex].URL
	})

	return links
}

func normalizeSchemaInputs(root string, inputs []string) ([]string, error) {
	normalized := make([]string, 0, len(inputs))

	for _, input := range inputs {
		absolute := input
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(root, absolute)
		}

		absolute, err := filepath.Abs(absolute)
		if err != nil {
			return nil, withErrorContext(err, "resolve schema input %s", input)
		}

		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, withErrorContext(err, "resolve schema input symlinks %s", input)
		}

		if !within(root, resolved) {
			return nil, linkFailureError(fmt.Sprintf("schema input %s escapes repository root", input))
		}

		normalized = append(normalized, resolved)
	}

	sort.Strings(normalized)

	return normalized, nil
}

func schemaFiles(inputs []string) ([]string, error) {
	var files []string

	for _, input := range inputs {
		info, err := os.Stat(input)
		if err != nil {
			return nil, withErrorContext(err, "inspect schema input %s", input)
		}

		if info.IsDir() {
			walkErr := filepath.WalkDir(input, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return withErrorContext(walkErr, "walk schema directory %s", path)
				}

				if entry.IsDir() {
					return nil
				}

				switch strings.ToLower(filepath.Ext(path)) {
				case ".yaml", ".yml", ".json", graphqlExtension, graphqlShortExtension:
					files = append(files, path)
				}

				return nil
			})
			if walkErr != nil {
				return nil, withErrorContext(walkErr, "walk schema input %s", input)
			}

			continue
		}

		switch strings.ToLower(filepath.Ext(input)) {
		case ".yaml", ".yml", ".json", graphqlExtension, graphqlShortExtension:
			files = append(files, input)
		}
	}

	sort.Strings(files)

	return slices.Compact(files), nil
}

func isGraphQLFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case graphqlExtension, graphqlShortExtension:
		return true
	default:
		return false
	}
}

func (c *linkCollector) scanGraphQL(path string) error {
	absPath, err := canonicalSchemaPath(c.repoRoot, path)
	if err != nil {
		return err
	}

	contents, err := os.ReadFile(absPath) // #nosec G304 -- GraphQL files are canonicalized under the repository root.
	if err != nil {
		return withErrorContext(err, "read GraphQL schema %s", absPath)
	}

	for lineIndex, line := range strings.Split(string(contents), "\n") {
		for _, value := range c.urlPattern.FindAllString(line, -1) {
			value = trimURLPunctuation(value)
			if value == "" {
				continue
			}

			c.add(schemaLink{
				URL:            value,
				Kind:           linkKindURL,
				Source:         relativeTo(c.repoRoot, absPath),
				Pointer:        "#/line/" + strconv.Itoa(lineIndex+1),
				Operation:      "",
				Operations:     nil,
				Line:           lineIndex + 1,
				ReferencedFrom: nil,
			})
		}
	}

	return nil
}

func (c *linkCollector) load(path string) ([]*yaml.Node, error) {
	absolute, err := canonicalSchemaPath(c.repoRoot, path)
	if err != nil {
		return nil, err
	}

	if cached, ok := c.cache[absolute]; ok {
		return cached.documents, nil
	}

	documents, err := decodeSchemaFile(absolute)
	if err != nil {
		return nil, err
	}

	c.cache[absolute] = yamlFile{documents: documents}

	return documents, nil
}

func canonicalSchemaPath(root, path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", withErrorContext(err, "resolve schema path %s", path)
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", withErrorContext(err, "resolve schema path symlinks %s", path)
	}

	if !within(root, resolved) {
		return "", linkFailureError(fmt.Sprintf("schema file %s escapes repository root", path))
	}

	return resolved, nil
}

func decodeSchemaFile(path string) ([]*yaml.Node, error) {
	file, err := os.Open(path) // #nosec G304 -- schema paths are canonicalized and constrained to the repository root.
	if err != nil {
		return nil, withErrorContext(err, "open schema file %s", path)
	}

	decoder := yaml.NewDecoder(file)

	var (
		documents []*yaml.Node
		decodeErr error
	)

	for {
		var document yaml.Node

		decodeErr = decoder.Decode(&document)
		if errors.Is(decodeErr, io.EOF) {
			decodeErr = nil

			break
		}

		if decodeErr != nil {
			break
		}

		documents = append(documents, &document)
	}

	closeErr := file.Close()

	if decodeErr != nil {
		return nil, withErrorContext(decodeErr, "decode schema file %s", path)
	}

	if closeErr != nil {
		return nil, withErrorContext(closeErr, "close schema file %s", path)
	}

	return documents, nil
}

func (c *linkCollector) walk(
	file string,
	node *yaml.Node,
	pointer string,
	referencedFrom []string,
	active map[string]bool,
) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.AliasNode:
		c.walk(file, node.Alias, pointer, referencedFrom, active)
	case yaml.MappingNode:
		c.walkMapping(file, node, pointer, referencedFrom, active)
	case yaml.SequenceNode:
		c.walkSequence(file, node, pointer, referencedFrom, active)
	case yaml.ScalarNode:
		c.walkScalar(file, node, pointer, referencedFrom)
	case yaml.DocumentNode:
		for _, child := range node.Content {
			c.walk(file, child, pointer, referencedFrom, active)
		}
	}
}

func (c *linkCollector) walkMapping(
	file string,
	node *yaml.Node,
	pointer string,
	referencedFrom []string,
	active map[string]bool,
) {
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index]
		value := node.Content[index+1]

		keyPath := pointer + "/" + escapePointer(key.Value)
		if key.Value == "$ref" && value.Kind == yaml.ScalarNode {
			c.walkReference(file, value.Value, keyPath, referencedFrom, active)

			continue
		}

		c.walk(file, value, keyPath, referencedFrom, active)
	}
}

func (c *linkCollector) walkSequence(
	file string,
	node *yaml.Node,
	pointer string,
	referencedFrom []string,
	active map[string]bool,
) {
	for index, child := range node.Content {
		c.walk(file, child, pointer+"/"+strconv.Itoa(index), referencedFrom, active)
	}
}

func (c *linkCollector) walkScalar(file string, node *yaml.Node, pointer string, referencedFrom []string) {
	if node.Tag != "!!str" {
		return
	}

	kind := linkKindForPointer(pointer)
	if kind == "" {
		return
	}

	values := c.urlPattern.FindAllString(node.Value, -1)
	if kind == linkKindExternalDocs && len(values) == 0 && strings.TrimSpace(node.Value) != "" {
		values = []string{strings.TrimSpace(node.Value)}
	}

	for _, value := range values {
		value = trimURLPunctuation(value)
		if value == "" {
			continue
		}

		c.add(schemaLink{
			URL:            value,
			Kind:           kind,
			Source:         relativeTo(c.repoRoot, file),
			Pointer:        pointer,
			Operation:      "",
			Operations:     nil,
			Line:           node.Line,
			ReferencedFrom: append([]string(nil), referencedFrom...),
		})
	}
}

func trimURLPunctuation(value string) string {
	value = strings.TrimRight(value, ".,;:")
	if !strings.Contains(value, "{") {
		value = strings.TrimRight(value, "}")
	}

	return value
}

func linkKindForPointer(pointer string) string {
	key := lastPointerPart(pointer)

	lowerPointer := strings.ToLower(pointer)

	switch {
	case key == "description":
		return linkKindDescription
	case key == "url" && strings.Contains(lowerPointer, "/externaldocs/"):
		return linkKindExternalDocs
	case strings.EqualFold(key, "url") || strings.HasSuffix(strings.ToLower(key), "url"):
		return linkKindURL
	default:
		return ""
	}
}

func (c *linkCollector) walkReference(
	fromFile string,
	reference string,
	ownerPointer string,
	referencedFrom []string,
	active map[string]bool,
) {
	absolute, targetPointer, external, err := resolveSchemaReference(c.repoRoot, fromFile, reference)
	if err != nil {
		c.errors = append(c.errors, err)

		return
	}

	if external {
		return
	}

	visitKey := absolute + targetPointer
	if active[visitKey] {
		return
	}

	documents, err := c.load(absolute)
	if err != nil {
		if !os.IsNotExist(err) {
			c.errors = append(c.errors, withErrorContext(
				err,
				"%s: resolve local schema reference %q",
				relativeTo(c.repoRoot, fromFile),
				reference,
			))
		}

		return
	}

	if len(documents) == 0 || len(documents[0].Content) == 0 {
		return
	}

	target := findPointer(documents[0].Content[0], targetPointer)
	if target == nil {
		return
	}

	provenance := append([]string(nil), referencedFrom...)
	provenance = append(provenance, relativeTo(c.repoRoot, fromFile)+ownerPointer)
	active[visitKey] = true
	c.walk(absolute, target, targetPointer, provenance, active)
	delete(active, visitKey)
}

func resolveSchemaReference(repoRoot, fromFile, reference string) (string, string, bool, error) {
	parsed, err := url.Parse(reference)
	if err != nil {
		return "", "", false, withErrorContext(
			err,
			"%s: invalid local schema reference %q",
			relativeTo(repoRoot, fromFile),
			reference,
		)
	}

	if parsed.Scheme != "" || parsed.Host != "" {
		return "", "", true, nil
	}

	targetFile := fromFile

	if parsed.Path != "" {
		pathPart, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return "", "", false, withErrorContext(
				err,
				"%s: invalid local schema reference %q",
				relativeTo(repoRoot, fromFile),
				reference,
			)
		}

		targetFile = filepath.Clean(filepath.Join(filepath.Dir(fromFile), filepath.FromSlash(pathPart)))
	}

	absolute, err := filepath.Abs(targetFile)
	if err != nil {
		return "", "", false, withErrorContext(err, "resolve local schema reference %q", reference)
	}

	if !within(repoRoot, absolute) {
		message := fmt.Sprintf(
			"%s: local schema reference %q escapes repository root",
			relativeTo(repoRoot, fromFile),
			reference,
		)

		return "", "", false, linkFailureError(message)
	}

	targetPointer, err := localSchemaReferencePointer(repoRoot, fromFile, reference, parsed)
	if err != nil {
		return "", "", false, err
	}

	return absolute, targetPointer, false, nil
}

func localSchemaReferencePointer(repoRoot, fromFile, reference string, parsed *url.URL) (string, error) {
	fragment, err := url.PathUnescape(parsed.Fragment)
	if err != nil {
		return "", withErrorContext(
			err,
			"%s: invalid local schema reference %q",
			relativeTo(repoRoot, fromFile),
			reference,
		)
	}

	targetPointer := "#"
	if fragment != "" {
		targetPointer += "/" + strings.TrimPrefix(fragment, "/")
	}

	return targetPointer, nil
}

func (c *linkCollector) add(link schemaLink) {
	link.Operations = operationIdentities(link)
	if len(link.Operations) > 0 {
		link.Operation = link.Operations[0]
	}

	key := link.Source + "\x00" + link.Pointer + "\x00" + link.URL + "\x00" + link.Kind
	if current, ok := c.links[key]; ok {
		current.ReferencedFrom = append(current.ReferencedFrom, link.ReferencedFrom...)
		current.Operations = append(current.Operations, link.Operations...)

		current.Operations = sortedUnique(current.Operations)
		if len(current.Operations) > 0 {
			current.Operation = current.Operations[0]
		}

		return
	}

	c.links[key] = &link
}

func operationIdentities(link schemaLink) []string {
	operations := make([]string, 0, len(link.ReferencedFrom)+1)

	operation := operationIdentity(link.Pointer)
	if operation != "" {
		operations = append(operations, operation)
	}

	for _, reference := range link.ReferencedFrom {
		operation = operationIdentity(reference)
		if operation != "" {
			operations = append(operations, operation)
		}
	}

	return sortedUnique(operations)
}

func sortedUnique(values []string) []string {
	sort.Strings(values)

	return slices.Compact(values)
}

func operationIdentity(pointer string) string {
	if hash := strings.LastIndex(pointer, "#"); hash >= 0 {
		pointer = pointer[hash:]
	}

	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(pointer, "#"), "/"), "/")
	for index, rawPart := range parts {
		if rawPart != "paths" || index+2 >= len(parts) {
			continue
		}

		path := strings.ReplaceAll(strings.ReplaceAll(parts[index+1], "~1", "/"), "~0", "~")

		method := strings.ToUpper(parts[index+2])
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
			if strings.HasPrefix(path, "/") {
				return method + " " + path
			}
		}
	}

	return ""
}

func findPointer(root *yaml.Node, pointer string) *yaml.Node {
	if pointer == "" || pointer == "#" {
		return root
	}

	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(pointer, "#"), "/"), "/")
	current := root

	for _, rawPart := range parts {
		part := strings.ReplaceAll(strings.ReplaceAll(rawPart, "~1", "/"), "~0", "~")

		current = pointerChild(current, part)
		if current == nil {
			return nil
		}
	}

	return current
}

func pointerChild(node *yaml.Node, part string) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	if node == nil {
		return nil
	}

	switch node.Kind {
	case yaml.DocumentNode:
		return documentContent(node)
	case yaml.MappingNode:
		return mappingValue(node, part)
	case yaml.SequenceNode:
		return sequenceValue(node, part)
	case yaml.ScalarNode, yaml.AliasNode:
		return nil
	default:
		return nil
	}
}

func documentContent(document *yaml.Node) *yaml.Node {
	if len(document.Content) != 1 {
		return nil
	}

	return document.Content[0]
}

func sequenceValue(sequence *yaml.Node, part string) *yaml.Node {
	index, err := strconv.Atoi(part)
	if err != nil || index < 0 || index >= len(sequence.Content) {
		return nil
	}

	return sequence.Content[index]
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}

	return nil
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")

	return strings.ReplaceAll(value, "/", "~1")
}

func lastPointerPart(pointer string) string {
	parts := strings.Split(pointer, "/")
	if len(parts) == 0 {
		return ""
	}

	return strings.ReplaceAll(strings.ReplaceAll(parts[len(parts)-1], "~1", "/"), "~0", "~")
}

func relativeTo(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(relative)
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)

	if err != nil || relative == ".." || filepath.IsAbs(relative) {
		return false
	}

	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
