// Package generatedapi contains the HTTP and WebSocket wire contracts generated
// from api/openapi.yaml and api/asyncapi.yaml. Run go generate ./pkg/generatedapi
// after changing either schema.
package generatedapi

//go:generate go run ../../tools/generateapi -openapi ../../api/openapi.yaml -asyncapi ../../api/asyncapi.yaml -out contracts.gen.go
