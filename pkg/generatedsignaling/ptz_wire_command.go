package generatedsignaling

type PtzWireCommand struct {
	Jsonrpc              string                 `json:"jsonrpc" binding:"required"`
	Id                   string                 `json:"id" binding:"required"`
	Method               string                 `json:"method" binding:"required"`
	Params               *PtzWireParams         `json:"params" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
