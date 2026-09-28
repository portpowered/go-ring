package replay

import (
	"strings"
	"testing"
)

func TestFrameTemplateBindsOnlyUUIDAndExactReferences(t *testing.T) {
	const first = `{"dialog_id":"$uuid:dialog","body":{"value":1}}`
	const good = `{"dialog_id":"30f4af1f-b705-42e7-ab2d-baf8bbca9657","body":{"value":1}}`
	for name, actual := range map[string]string{
		"non UUID":      `{"dialog_id":"anything","body":{"value":1}}`,
		"extra key":     `{"dialog_id":"30f4af1f-b705-42e7-ab2d-baf8bbca9657","body":{"value":1,"extra":true}}`,
		"changed value": `{"dialog_id":"30f4af1f-b705-42e7-ab2d-baf8bbca9657","body":{"value":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := matchFrameTemplate([]byte(first), []byte(actual), map[string]string{}); err == nil {
				t.Fatal("template accepted invalid frame")
			}
		})
	}
	bindings := map[string]string{}
	if err := matchFrameTemplate([]byte(first), []byte(good), bindings); err != nil {
		t.Fatal(err)
	}
	if err := matchFrameTemplate([]byte(`{"dialog_id":"$ref:dialog"}`), []byte(`{"dialog_id":"different"}`), bindings); err == nil {
		t.Fatal("template accepted a changed binding")
	}
	server, err := renderFrameTemplate([]byte(`{"dialog_id":"$ref:dialog"}`), bindings)
	if err != nil || !strings.Contains(string(server), bindings["dialog"]) {
		t.Fatalf("rendered response = %s, %v", server, err)
	}
	if _, err := renderFrameTemplate([]byte(`{"dialog_id":"$ref:missing"}`), bindings); err == nil {
		t.Fatal("template rendered an unbound reference")
	}
}
