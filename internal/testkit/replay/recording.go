package replay

import (
	"encoding/json"
	"os"
)

// RecordedMessage preserves a sanitized wire frame and its direction. Tests
// select the exchange needed for a behavior instead of replaying a whole stream.
type RecordedMessage struct {
	Direction string          `json:"direction"`
	Frame     string          `json:"frame"`
	Payload   json.RawMessage `json:"payload"`
}

type SessionRecording struct {
	Messages []RecordedMessage `json:"messages"`
}

func LoadSessionRecording(path string) (SessionRecording, error) {
	var recording SessionRecording
	b, err := os.ReadFile(path)
	if err != nil {
		return recording, err
	}
	err = json.Unmarshal(b, &recording)
	return recording, err
}

// LoadCases reads portable synthetic case arrays used by both language suites.
func LoadCases[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []T
	err = json.Unmarshal(b, &cases)
	return cases, err
}
