package routegate

import "regexp"

func readAsyncModel(path string) (asyncModel, error) {
	var rootDocument map[string]any

	err := readYAML(path, &rootDocument)
	if err != nil {
		return asyncModel{}, err
	}

	model := asyncModel{
		channels:   make(map[string]asyncChannel),
		servers:    make(map[string]asyncServer),
		operations: make(map[string]asyncOperation),
		messages:   make(map[string]asyncMessage),
		schemas:    make(map[string]asyncSchema),
	}

	components := stringMap(rootDocument["components"])

	for name, value := range stringMap(rootDocument["servers"]) {
		server := stringMap(value)
		model.servers[name] = asyncServer{
			Host:     stringValue(server["host"]),
			Protocol: stringValue(server["protocol"]),
		}
	}

	for name, value := range stringMap(rootDocument["channels"]) {
		channelValue := stringMap(value)

		channel := asyncChannel{
			Address:  stringValue(channelValue["address"]),
			Server:   "",
			Messages: make(map[string]string),
			Query:    emptyAsyncQuery(),
			HasQuery: false,
		}
		bindings := stringMap(channelValue["bindings"])

		websocketBinding := stringMap(bindings["ws"])
		if query, exists := websocketBinding["query"]; exists {
			channel.HasQuery = true

			var valid bool

			channel.Query, valid = parseAsyncQuery(query)
			if !valid {
				return asyncModel{}, newRouteGateError("AsyncAPI channel %q has an unsupported WebSocket query binding", name)
			}
		}

		if serverRefs, ok := channelValue["servers"].([]any); ok && len(serverRefs) == 1 {
			channel.Server = refName(stringValue(stringMap(serverRefs[0])["$ref"]))
		}

		for messageName, messageValue := range stringMap(channelValue["messages"]) {
			channel.Messages[messageName] = refName(stringValue(stringMap(messageValue)["$ref"]))
		}

		model.channels[name] = channel
	}

	for name, value := range stringMap(rootDocument["operations"]) {
		operationValue := stringMap(value)
		model.operations[name] = asyncOperation{
			Action:  stringValue(operationValue["action"]),
			Channel: refName(stringValue(stringMap(operationValue["channel"])["$ref"])),
		}
	}

	for name, value := range stringMap(stringMap(components["messages"])) {
		messageValue := stringMap(value)
		payload := stringMap(messageValue["payload"])
		model.messages[name] = asyncMessage{Frame: refName(stringValue(payload["$ref"]))}
	}

	for name, value := range stringMap(stringMap(components["schemas"])) {
		schemaValue := stringMap(value)
		properties := stringMap(schemaValue["properties"])
		method := stringValue(stringMap(properties["method"])["const"])

		body := refName(stringValue(stringMap(properties["body"])["$ref"]))

		model.schemas[name] = asyncSchema{Method: method, Body: body}
	}

	for name, operation := range model.operations {
		if operation.Action == "send" && operation.Channel == "" {
			return asyncModel{}, newRouteGateError("AsyncAPI send operation %q has no channel reference", name)
		}
	}

	return model, nil
}

func parseAsyncQuery(value any) (AsyncQuery, bool) {
	query := stringMap(value)
	if query == nil || stringValue(query["type"]) != "object" {
		return emptyAsyncQuery(), false
	}

	result := AsyncQuery{
		Required:             nil,
		Properties:           make(map[string]AsyncQueryProperty),
		AdditionalProperties: true,
	}

	for _, required := range anySlice(query["required"]) {
		name, ok := required.(string)
		if !ok || name == "" {
			return emptyAsyncQuery(), false
		}

		result.Required = append(result.Required, name)
	}

	for name, propertyValue := range stringMap(query["properties"]) {
		property := stringMap(propertyValue)
		if property == nil {
			return emptyAsyncQuery(), false
		}

		parsed := AsyncQueryProperty{
			Type:      stringValue(property["type"]),
			Const:     stringValue(property["const"]),
			Enum:      nil,
			Pattern:   stringValue(property["pattern"]),
			MinLength: 0,
		}
		if parsed.Type != "" && parsed.Type != "string" {
			return emptyAsyncQuery(), false
		}

		if parsed.Pattern != "" {
			_, err := regexp.Compile(parsed.Pattern)
			if err != nil {
				return emptyAsyncQuery(), false
			}
		}

		if minLength, ok := integerValue(property["minLength"]); ok {
			parsed.MinLength = minLength
		} else if property["minLength"] != nil {
			return emptyAsyncQuery(), false
		}

		for _, enumValue := range anySlice(property["enum"]) {
			text, ok := enumValue.(string)
			if !ok {
				return emptyAsyncQuery(), false
			}

			parsed.Enum = append(parsed.Enum, text)
		}

		result.Properties[name] = parsed
	}

	if value, exists := query["additionalProperties"]; exists {
		allowed, ok := value.(bool)
		if !ok {
			return emptyAsyncQuery(), false
		}

		result.AdditionalProperties = allowed
	}

	return result, true
}

func emptyAsyncQuery() AsyncQuery {
	return AsyncQuery{
		Required:             nil,
		Properties:           nil,
		AdditionalProperties: false,
	}
}

func integerValue(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case uint64:
		if number > uint64(^uint(0)>>1) {
			return 0, false
		}

		return int(number), true
	default:
		return 0, false
	}
}
