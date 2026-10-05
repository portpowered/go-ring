package main

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func (v *validator) validateAsyncAPIExamplesAndGroups() {
	for _, doc := range v.sortedDocuments() {
		root, ok := asMap(doc.data)
		if !ok {
			continue
		}

		if _, isAsyncAPI := root["asyncapi"]; !isAsyncAPI {
			continue
		}

		seen := make(map[string]bool)

		for _, message := range v.asyncAPIMessages(doc, root) {
			key := message.doc.uri + message.pointer
			if seen[key] {
				continue
			}

			seen[key] = true

			v.validateAsyncAPIMessage(message)
		}
	}
}

func (v *validator) asyncAPIMessages(doc *document, root map[string]any) []location {
	var result []location

	add := func(pointer string, value any) {
		v.collectAsyncMessageLocations(doc, pointer, value, make(map[string]bool), &result)
	}
	collectAsyncComponentMessages(root, add)
	collectAsyncChannelMessages(root, add)
	collectAsyncOperationMessages(root, add)

	return result
}

func collectAsyncComponentMessages(root map[string]any, add func(string, any)) {
	components, componentsAreMap := asMap(root["components"])
	if !componentsAreMap {
		return
	}

	messages, ok := asMap(components["messages"])
	if !ok {
		return
	}

	for _, name := range sortedKeys(messages) {
		pointer := joinPointer(joinPointer(joinPointer("#", "components"), "messages"), name)
		add(pointer, messages[name])
	}
}

func collectAsyncChannelMessages(root map[string]any, add func(string, any)) {
	channels, ok := asMap(root["channels"])
	if !ok {
		return
	}

	for _, channelName := range sortedKeys(channels) {
		channelPointer := joinPointer(joinPointer("#", "channels"), channelName)

		channel, ok := asMap(channels[channelName])
		if !ok {
			continue
		}

		collectAsyncChannelNamedMessages(channelPointer, channel, add)
		collectAsyncChannelOperationMessages(channelPointer, channel, add)
	}
}

func collectAsyncChannelNamedMessages(channelPointer string, channel map[string]any, add func(string, any)) {
	messages, ok := asMap(channel["messages"])
	if !ok {
		return
	}

	for _, messageName := range sortedKeys(messages) {
		pointer := joinPointer(joinPointer(channelPointer, "messages"), messageName)
		add(pointer, messages[messageName])
	}
}

func collectAsyncChannelOperationMessages(channelPointer string, channel map[string]any, add func(string, any)) {
	for _, action := range []string{"publish", "subscribe"} {
		operation, ok := asMap(channel[action])
		if !ok || !hasKey(operation, "message") {
			continue
		}

		pointer := joinPointer(joinPointer(channelPointer, action), "message")
		add(pointer, operation["message"])
	}
}

func collectAsyncOperationMessages(root map[string]any, add func(string, any)) {
	operations, ok := asMap(root["operations"])
	if !ok {
		return
	}

	for _, operationName := range sortedKeys(operations) {
		operation, ok := asMap(operations[operationName])
		if !ok {
			continue
		}

		operationPointer := joinPointer(joinPointer("#", "operations"), operationName)
		collectAsyncOperationNamedMessages(operationPointer, operation["messages"], add)
	}
}

func collectAsyncOperationNamedMessages(operationPointer string, value any, add func(string, any)) {
	messagesPointer := joinPointer(operationPointer, "messages")

	switch messages := value.(type) {
	case []any:
		for index, message := range messages {
			add(joinPointer(messagesPointer, strconv.Itoa(index)), message)
		}
	case map[string]any:
		for _, messageName := range sortedKeys(messages) {
			add(joinPointer(messagesPointer, messageName), messages[messageName])
		}
	}
}

func (v *validator) collectAsyncMessageLocations(
	doc *document,
	pointer string,
	value any,
	seen map[string]bool,
	result *[]location,
) {
	object, ok := asMap(value)
	if !ok {
		return
	}

	if v.collectAsyncMessageReference(doc, pointer, object, seen, result) {
		return
	}

	if v.collectAsyncMessageAlternatives(doc, pointer, object, seen, result) {
		return
	}

	if hasKey(object, "payload") {
		*result = append(*result, location{doc: doc, pointer: pointer})
	}
}

func (v *validator) collectAsyncMessageReference(
	doc *document,
	pointer string,
	object map[string]any,
	seen map[string]bool,
	result *[]location,
) bool {
	ref, ok := object["$ref"].(string)
	if !ok {
		return false
	}

	key := doc.uri + pointer + ref
	if seen[key] {
		return true
	}

	seen[key] = true

	targetDoc, targetPointer, target, err := v.resolveReference(doc, ref)
	if err != nil {
		v.addIssue(doc.path, pointer, "unresolved AsyncAPI message reference")

		return true
	}

	v.collectAsyncMessageLocations(targetDoc, targetPointer, target, seen, result)

	return true
}

func (v *validator) collectAsyncMessageAlternatives(
	doc *document,
	pointer string,
	object map[string]any,
	seen map[string]bool,
	result *[]location,
) bool {
	alternatives, ok := object["oneOf"].([]any)
	if !ok {
		return false
	}

	for index, alternative := range alternatives {
		alternativePointer := joinPointer(joinPointer(pointer, "oneOf"), strconv.Itoa(index))
		v.collectAsyncMessageLocations(doc, alternativePointer, alternative, seen, result)
	}

	return true
}

func (v *validator) validateAsyncAPIMessage(message location) {
	value, found := resolvePointer(message.doc.data, message.pointer)
	if !found {
		v.addIssue(message.doc.path, message.pointer, "unresolved AsyncAPI message owner")

		return
	}

	object, isObject := asMap(value)
	if !isObject {
		return
	}

	payload, hasPayload := object["payload"]
	if !hasPayload {
		if nonEmptyExamples(object[examplesKey]) {
			examplesPointer := joinPointer(message.pointer, examplesKey)
			v.addIssue(message.doc.path, examplesPointer, "AsyncAPI examples have no payload schema owner")
		}

		return
	}

	payloadPointer := joinPointer(message.pointer, "payload")
	payloadOwner := location{doc: message.doc, pointer: payloadPointer}
	v.schemaNode(message.doc, payload, payloadPointer)

	var headersOwner location

	headers, hasHeaders := object["headers"]
	if hasHeaders {
		headersOwner = location{doc: message.doc, pointer: joinPointer(message.pointer, "headers")}
		v.schemaNode(message.doc, headers, headersOwner.pointer)
	}

	messageName := messageNameAt(message.pointer)
	group := "AsyncAPI message " + messageName
	v.report.groups++
	messageEvidence := object[exampleEvidenceKey]

	foundExample := v.validateAsyncAPIExamples(message, object, payloadOwner, headersOwner, hasHeaders, messageEvidence)
	if !foundExample && !v.rootExampleExists(payloadOwner, make(map[string]bool)) {
		v.addIssue(message.doc.path, payloadPointer, "missing required sample group: "+group)
	}
}

func (v *validator) validateAsyncAPIExamples(
	message location,
	object map[string]any,
	payloadOwner location,
	headersOwner location,
	hasHeaders bool,
	evidence any,
) bool {
	examples, exists := object[examplesKey]
	if !exists {
		return false
	}

	list, validList := examples.([]any)
	if !validList {
		v.addIssue(message.doc.path, joinPointer(message.pointer, examplesKey), "AsyncAPI message examples must be an array")

		return false
	}

	foundExample := false

	for index, raw := range list {
		examplePointer := joinPointer(joinPointer(message.pointer, examplesKey), strconv.Itoa(index))
		v.validateAsyncAPIExample(
			message,
			payloadOwner,
			headersOwner,
			hasHeaders,
			evidence,
			raw,
			examplePointer,
		)

		foundExample = true
	}

	return foundExample
}

func (v *validator) validateAsyncAPIExample(
	message location,
	payloadOwner location,
	headersOwner location,
	hasHeaders bool,
	evidence any,
	raw any,
	examplePointer string,
) {
	exampleDoc, resolvedPointer, resolved, err := v.resolveExampleObject(message.doc, raw, examplePointer)
	if err != nil {
		v.addIssue(message.doc.path, examplePointer, "unresolved AsyncAPI example owner reference")

		return
	}

	example, isObject := asMap(resolved)
	if !isObject {
		v.addIssue(exampleDoc.path, resolvedPointer, "AsyncAPI example must be an object")

		return
	}

	if hasKey(example, exampleEvidenceKey) {
		evidencePointer := joinPointer(resolvedPointer, exampleEvidenceKey)
		v.addIssue(exampleDoc.path, evidencePointer, "AsyncAPI Example Object evidence belongs on the Message Object")
	}

	v.validateAsyncAPIExamplePayload(payloadOwner, exampleDoc, resolvedPointer, example, evidence)
	v.validateAsyncAPIExampleHeaders(headersOwner, hasHeaders, exampleDoc, resolvedPointer, example, evidence)
}

func (v *validator) validateAsyncAPIExamplePayload(
	payloadOwner location,
	exampleDoc *document,
	resolvedPointer string,
	example map[string]any,
	evidence any,
) {
	samplePayload, hasSamplePayload := example["payload"]
	if !hasSamplePayload {
		v.addIssue(exampleDoc.path, resolvedPointer, "AsyncAPI example requires a payload")

		return
	}

	samplePointer := joinPointer(resolvedPointer, "payload")
	v.validateValue(payloadOwner, samplePayload, samplePointer, evidence)
}

func (v *validator) validateAsyncAPIExampleHeaders(
	headersOwner location,
	hasHeaders bool,
	exampleDoc *document,
	resolvedPointer string,
	example map[string]any,
	evidence any,
) {
	sampleHeaders, hasSampleHeaders := example["headers"]
	if !hasSampleHeaders {
		return
	}

	headerPointer := joinPointer(resolvedPointer, "headers")
	if !hasHeaders {
		v.addIssue(exampleDoc.path, headerPointer, "AsyncAPI example headers have no schema owner")

		return
	}

	v.validateValue(headersOwner, sampleHeaders, headerPointer, evidence)
}

func messageNameAt(pointer string) string {
	parts := strings.Split(strings.TrimPrefix(pointer, "#/"), "/")
	if len(parts) == 0 {
		return "<unnamed>"
	}

	last := parts[len(parts)-1]
	last = strings.ReplaceAll(strings.ReplaceAll(last, "~1", "/"), "~0", "~")

	return last
}

func (v *validator) rejectUnownedExampleComponents() {
	for _, doc := range v.sortedDocuments() {
		root, isObject := asMap(doc.data)
		if !isObject {
			continue
		}

		components, componentsAreMap := asMap(root["components"])
		if !componentsAreMap {
			continue
		}

		examples, ok := asMap(components[examplesKey])
		if !ok {
			continue
		}

		for _, name := range sortedKeys(examples) {
			pointer := joinPointer(joinPointer(joinPointer("#", "components"), examplesKey), name)
			if !v.usedExampleObjects[doc.uri+pointer] {
				v.addIssue(doc.path, pointer, "Example Object has no schema owner")
			}
		}
	}
}

func (v *validator) sortedDocuments() []*document {
	result := make([]*document, 0, len(v.docs))
	for _, doc := range v.docs {
		result = append(result, doc)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].path < result[j].path })

	return result
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

func safePointerFragment(pointer string) string {
	if strings.HasPrefix(pointer, "#") {
		return pointer
	}

	return "#" + pointer
}

func pointerURL(doc *document, pointer string) string {
	fragment := strings.TrimPrefix(safePointerFragment(pointer), "#")

	parts := strings.Split(fragment, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}

	return doc.uri + "#" + strings.Join(parts, "/")
}
