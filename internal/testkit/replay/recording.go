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

	b, err := os.ReadFile(path) // #nosec G304 -- test replay paths are supplied by the test harness.
	if err != nil {
		return recording, wrapReplayError("read signaling session recording", err)
	}

	err = json.Unmarshal(b, &recording)
	if err != nil {
		return SessionRecording{}, wrapReplayError("decode signaling session recording", err)
	}

	return recording, nil
}

// LoadCases reads portable synthetic case arrays used by both language suites.
func LoadCases[T any](path string) ([]T, error) {
	recordingBytes, err := os.ReadFile(path) // #nosec G304 -- test case paths are supplied by the test harness.
	if err != nil {
		return nil, wrapReplayError("read portable replay cases", err)
	}

	var cases []T

	err = json.Unmarshal(recordingBytes, &cases)
	if err != nil {
		return nil, wrapReplayError("decode portable replay cases", err)
	}

	return cases, nil
}
