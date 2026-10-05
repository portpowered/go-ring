package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var bundledReference = regexp.MustCompile(`\./(?:schemas/)?[A-Za-z0-9-]+\.yaml(#/components/[A-Za-z]+/[A-Za-z0-9_-]+)`)

func bundledSections() []string {
	return []string{"parameters", "requestBodies", "responses", "schemas"}
}

type componentKey struct {
	section string
	name    string
}

type componentBlock struct {
	name string
	raw  string
}

type schemaFragment struct {
	group      string
	path       string
	components map[string]map[string]struct{}
	blocks     map[string][]componentBlock
}

func bundleOpenAPI(basePath, fragmentsDir, outputPath string) error {
	base, baseDoc, err := readOpenAPIBase(basePath)
	if err != nil {
		return err
	}

	fragments, owners, err := readOpenAPIFragments(fragmentsDir)
	if err != nil {
		return err
	}

	err = validateBaseReferences(basePath, baseDoc, owners)
	if err != nil {
		return err
	}

	return writeOpenAPIBundle(base, fragments, outputPath)
}

func readOpenAPIBase(basePath string) (string, *yaml.Node, error) {
	baseBytes, err := readLocalFile(basePath)
	if err != nil {
		return "", nil, errorf("read OpenAPI base %s: %w", basePath, err)
	}

	base := normalizeLines(string(baseBytes))

	baseDoc, err := parseYAML(baseBytes, basePath)
	if err != nil {
		return "", nil, err
	}

	baseComponents := mappingValue(documentRoot(baseDoc), "components")
	if baseComponents == nil || mappingValue(baseComponents, "securitySchemes") == nil {
		return "", nil, errorf("%s must retain the Ring security scheme under components", basePath)
	}

	for _, section := range bundledSections() {
		if mappingValue(baseComponents, section) != nil {
			return "", nil, errorf(
				"%s contains components.%s; move that section into an API responsibility fragment",
				basePath,
				section,
			)
		}
	}

	return base, baseDoc, nil
}

func readOpenAPIFragments(fragmentsDir string) ([]schemaFragment, map[componentKey]string, error) {
	paths, err := filepath.Glob(filepath.Join(fragmentsDir, "*.yaml"))
	if err != nil {
		return nil, nil, errorf("list OpenAPI responsibility fragments: %w", err)
	}

	sort.Strings(paths)

	if len(paths) == 0 {
		return nil, nil, errorf("no OpenAPI responsibility fragments found in %s", fragmentsDir)
	}

	fragments := make([]schemaFragment, 0, len(paths))
	owners := make(map[componentKey]string)

	for _, path := range paths {
		fragment, err := readFragment(path)
		if err != nil {
			return nil, nil, err
		}

		for section, names := range fragment.components {
			for name := range names {
				key := componentKey{section: section, name: name}
				if previous, exists := owners[key]; exists {
					return nil, nil, errorf("component %s/%s is owned by both %s and %s", section, name, previous, fragment.group)
				}

				owners[key] = fragment.group
			}
		}

		fragments = append(fragments, fragment)
	}

	for _, fragment := range fragments {
		err := validateFragmentReferences(fragment, owners)
		if err != nil {
			return nil, nil, err
		}
	}

	return fragments, owners, nil
}

func writeOpenAPIBundle(base string, fragments []schemaFragment, outputPath string) error {
	var output strings.Builder

	base = bundledReference.ReplaceAllString(base, "$1")
	output.WriteString(strings.TrimRight(base, "\n"))
	output.WriteByte('\n')

	for _, section := range bundledSections() {
		var blocks []string
		for _, fragment := range fragments {
			for _, block := range fragment.blocks[section] {
				blocks = append(blocks, bundledReference.ReplaceAllString(block.raw, "$1"))
			}
		}

		if len(blocks) == 0 {
			continue
		}

		output.WriteString("\n  ")
		output.WriteString(section)
		output.WriteString(":\n")

		for _, block := range blocks {
			output.WriteString(strings.TrimRight(block, "\n"))
			output.WriteString("\n\n")
		}
	}

	assembled := strings.TrimRight(output.String(), "\n") + "\n"

	_, err := parseYAML([]byte(assembled), outputPath)
	if err != nil {
		return errorf("assembled OpenAPI bundle is invalid: %w", err)
	}

	err = os.WriteFile(outputPath, []byte(assembled), 0o600)
	if err != nil {
		return errorf("write OpenAPI bundle %s: %w", outputPath, err)
	}

	return nil
}

func readFragment(path string) (schemaFragment, error) {
	data, err := readLocalFile(path)
	if err != nil {
		return schemaFragment{}, errorf("read responsibility fragment %s: %w", path, err)
	}

	doc, err := parseYAML(data, path)
	if err != nil {
		return schemaFragment{}, err
	}

	root := documentRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return schemaFragment{}, errorf("%s must contain a YAML mapping", path)
	}

	components := mappingValue(root, "components")
	if components == nil || components.Kind != yaml.MappingNode {
		return schemaFragment{}, errorf("%s must contain a components mapping", path)
	}

	for rootEntryIndex := 0; rootEntryIndex+1 < len(root.Content); rootEntryIndex += 2 {
		if root.Content[rootEntryIndex].Value != "components" {
			return schemaFragment{}, errorf("%s contains unsupported top-level key %q", path, root.Content[rootEntryIndex].Value)
		}
	}

	group := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	fragment := schemaFragment{
		group:      group,
		path:       path,
		components: make(map[string]map[string]struct{}),
		blocks:     make(map[string][]componentBlock),
	}

	for componentIndex := 0; componentIndex+1 < len(components.Content); componentIndex += 2 {
		section := components.Content[componentIndex].Value
		if !contains(bundledSections(), section) {
			return schemaFragment{}, errorf("%s contains unsupported component section %q", path, section)
		}

		entries := components.Content[componentIndex+1]
		if entries.Kind != yaml.MappingNode {
			return schemaFragment{}, errorf("%s components.%s must be a mapping", path, section)
		}

		sectionNames := make(map[string]struct{}, len(entries.Content)/2)
		for entry := 0; entry+1 < len(entries.Content); entry += 2 {
			sectionNames[entries.Content[entry].Value] = struct{}{}
		}

		fragment.components[section] = sectionNames
	}

	fragment.blocks = rawComponentBlocks(normalizeLines(string(data)))
	for section, names := range fragment.components {
		blocks := fragment.blocks[section]
		if len(blocks) != len(names) {
			return schemaFragment{}, errorf(
				"%s components.%s has %d YAML entries but %d source blocks",
				path,
				section,
				len(names),
				len(blocks),
			)
		}

		for _, block := range blocks {
			if _, exists := names[block.name]; !exists {
				return schemaFragment{}, errorf(
					"%s components.%s source block %q is not in the parsed component map",
					path,
					section,
					block.name,
				)
			}
		}
	}

	return fragment, nil
}

func rawComponentBlocks(source string) map[string][]componentBlock {
	lines := strings.Split(source, "\n")
	blocks := make(map[string][]componentBlock)
	section := ""
	blockStart := -1
	blockName := ""
	finish := func(end int) {
		if blockStart < 0 {
			return
		}

		raw := strings.TrimRight(strings.Join(lines[blockStart:end], "\n"), "\n") + "\n"
		blocks[section] = append(blocks[section], componentBlock{name: blockName, raw: raw})
		blockStart = -1
		blockName = ""
	}

	for lineIndex, line := range lines {
		indent := leadingSpaces(line)

		trimmed := strings.TrimSpace(line)

		if indent == 2 && strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "#") {
			finish(lineIndex)

			section = strings.TrimSuffix(trimmed, ":")

			continue
		}

		if section == "" || indent != 4 || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key, _, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}

		finish(lineIndex)
		blockStart = lineIndex
		blockName = unquoteYAMLKey(strings.TrimSpace(key))
	}

	finish(len(lines))

	return blocks
}

func validateBaseReferences(path string, doc *yaml.Node, owners map[componentKey]string) error {
	components := mappingValue(documentRoot(doc), "components")
	if components == nil || mappingValue(components, "securitySchemes") == nil {
		return errorf("%s has no local components.securitySchemes mapping", path)
	}

	var visit func(*yaml.Node) error

	visit = func(node *yaml.Node) error {
		if node.Kind == yaml.MappingNode {
			for entryIndex := 0; entryIndex+1 < len(node.Content); entryIndex += 2 {
				key, value := node.Content[entryIndex], node.Content[entryIndex+1]
				if key.Value == "$ref" && value.Kind == yaml.ScalarNode {
					err := validateBaseReference(value.Value, components, owners)
					if err != nil {
						return errorf("%s: %w", path, err)
					}
				}

				err := visit(value)
				if err != nil {
					return err
				}
			}

			return nil
		}

		for _, child := range node.Content {
			err := visit(child)
			if err != nil {
				return err
			}
		}

		return nil
	}

	return visit(documentRoot(doc))
}

func validateBaseReference(ref string, components *yaml.Node, owners map[componentKey]string) error {
	if strings.HasPrefix(ref, "#/components/securitySchemes/") {
		parts := strings.Split(strings.TrimPrefix(ref, "#/components/securitySchemes/"), "/")
		if len(parts) != 1 || mappingValue(mappingValue(components, "securitySchemes"), parts[0]) == nil {
			return errorf("local security scheme reference %q does not resolve in api/openapi.base.yaml", ref)
		}

		return nil
	}

	if !strings.HasPrefix(ref, "./schemas/") {
		return errorf(
			"canonical OpenAPI base reference %q must target a local security scheme or ./schemas/<group>.yaml component",
			ref,
		)
	}

	file, remainder, ok := strings.Cut(strings.TrimPrefix(ref, "./schemas/"), "#")
	if !ok || filepath.Ext(file) != ".yaml" || strings.Contains(file, "/") || strings.Contains(file, "\\") {
		return errorf("relative base component reference %q must target one schema-group file", ref)
	}

	group := strings.TrimSuffix(file, filepath.Ext(file))

	return validateComponentReference(group, "#"+remainder, owners)
}

func validateFragmentReferences(fragment schemaFragment, owners map[componentKey]string) error {
	data, err := readLocalFile(fragment.path)
	if err != nil {
		return errorf("read responsibility fragment refs %s: %w", fragment.path, err)
	}

	doc, err := parseYAML(data, fragment.path)
	if err != nil {
		return err
	}

	var visit func(*yaml.Node) error

	visit = func(node *yaml.Node) error {
		if node.Kind == yaml.MappingNode {
			for entryIndex := 0; entryIndex+1 < len(node.Content); entryIndex += 2 {
				key, value := node.Content[entryIndex], node.Content[entryIndex+1]
				if key.Value == "$ref" && value.Kind == yaml.ScalarNode {
					err := validateComponentReference(fragment.group, value.Value, owners)
					if err != nil {
						return errorf("%s: %w", fragment.path, err)
					}
				}

				err := visit(value)
				if err != nil {
					return err
				}
			}

			return nil
		}

		for _, child := range node.Content {
			err := visit(child)
			if err != nil {
				return err
			}
		}

		return nil
	}

	return visit(documentRoot(doc))
}

func validateComponentReference(ownerGroup, ref string, owners map[componentKey]string) error {
	targetGroup := ownerGroup

	fragmentRef := ref

	if strings.HasPrefix(ref, "./") {
		file, remainder, ok := strings.Cut(strings.TrimPrefix(ref, "./"), "#")
		if !ok || filepath.Ext(file) != ".yaml" || strings.Contains(file, "/") || strings.Contains(file, "\\") {
			return errorf("relative component reference %q must target a sibling responsibility fragment", ref)
		}

		targetGroup = strings.TrimSuffix(file, filepath.Ext(file))
		fragmentRef = "#" + remainder
	}

	if !strings.HasPrefix(fragmentRef, "#/components/") {
		return errorf("component reference %q must be local or use a sibling fragment ref", ref)
	}

	parts := strings.Split(strings.TrimPrefix(fragmentRef, "#/components/"), "/")
	if len(parts) != 2 {
		return errorf("component reference %q must name one component", ref)
	}

	section, name := parts[0], parts[1]
	if !contains(bundledSections(), section) {
		return errorf("component reference %q uses unsupported component section %q", ref, section)
	}

	actualOwner, exists := owners[componentKey{section: section, name: name}]
	if !exists {
		return errorf("component reference %q has no canonical owner", ref)
	}

	if targetGroup != actualOwner {
		return errorf(
			"component reference %q points to %s, owned by %s",
			ref,
			targetGroup,
			actualOwner,
		)
	}

	return nil
}

func parseYAML(data []byte, name string) (*yaml.Node, error) {
	var doc yaml.Node

	err := yaml.Unmarshal(data, &doc)
	if err != nil {
		return nil, errorf("parse YAML %s: %w", name, err)
	}

	if documentRoot(&doc) == nil {
		return nil, errorf("YAML document has no root node")
	}

	return &doc, nil
}

func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil
	}

	return doc.Content[0]
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}

	for entryIndex := 0; entryIndex+1 < len(mapping.Content); entryIndex += 2 {
		if mapping.Content[entryIndex].Value == key {
			return mapping.Content[entryIndex+1]
		}
	}

	return nil
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func unquoteYAMLKey(key string) string {
	if len(key) >= 2 && ((key[0] == '\'' && key[len(key)-1] == '\'') || (key[0] == '"' && key[len(key)-1] == '"')) {
		return key[1 : len(key)-1]
	}

	return key
}

func normalizeLines(source string) string {
	return strings.ReplaceAll(source, "\r\n", "\n")
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}

	return false
}
