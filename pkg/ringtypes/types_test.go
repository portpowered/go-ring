package ringtypes_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ringtypes"
)

func TestBatteryReadingUnmarshalJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		input   string
		initial ringtypes.BatteryReading
		want    ringtypes.BatteryReading
	}{
		{name: "null preserves value", input: "null", initial: 42, want: 42},
		{name: "number", input: "73.5", initial: 0, want: 73.5},
		{name: "numeric text", input: `"73.5"`, initial: 0, want: 73.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value := test.initial

			err := json.Unmarshal([]byte(test.input), &value)
			if err != nil {
				t.Fatal(err)
			}

			if value != test.want {
				t.Fatalf("battery reading = %v, want %v", value, test.want)
			}
		})
	}

	for _, input := range []string{`"not-a-number"`, `{"value":1}`} {
		value := ringtypes.BatteryReading(42)

		err := json.Unmarshal([]byte(input), &value)
		if err == nil {
			t.Fatalf("invalid battery reading %s succeeded", input)
		}

		if !strings.Contains(err.Error(), "battery reading") {
			t.Fatalf("invalid battery error = %v", err)
		}

		if errors.Unwrap(err) == nil || value != 42 {
			t.Fatalf("invalid battery reading did not preserve its cause and value: %v, %v", err, value)
		}
	}
}

func TestOwnerIDUnmarshalJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		input string
		want  ringtypes.OwnerID
	}{
		{name: "number", input: "1000", want: "1000"},
		{name: "text", input: `"owner-1"`, want: "owner-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var value ringtypes.OwnerID

			err := json.Unmarshal([]byte(test.input), &value)
			if err != nil {
				t.Fatal(err)
			}

			if value != test.want {
				t.Fatalf("owner ID = %q, want %q", value, test.want)
			}
		})
	}

	var value ringtypes.OwnerID

	err := json.Unmarshal([]byte("true"), &value)
	if err == nil {
		t.Fatal("boolean owner ID succeeded")
	}

	if !strings.Contains(err.Error(), "owner identifier") {
		t.Fatalf("invalid owner ID error = %v", err)
	}

	if errors.Unwrap(err) == nil {
		t.Fatal("invalid owner ID did not preserve its cause")
	}

	err = value.UnmarshalJSON(nil)
	if err == nil {
		t.Fatal("empty owner ID JSON succeeded")
	}
}
