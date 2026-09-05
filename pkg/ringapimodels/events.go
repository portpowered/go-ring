package ringapimodels

// Event represents a Ring event
type Event struct {
	Kind      EventKind              `json:"kind"` // "motion", "ding", "on_demand"
	DeviceID  int64                  `json:"device_id"`
	Timestamp string                 `json:"timestamp"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// EventCallback is a function type for handling events
type EventCallback func(*Event) error
