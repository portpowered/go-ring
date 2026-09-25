package generatedsignaling

type PlaybackOfferBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	EntryPoint           string                 `json:"entry_point" binding:"required"`
	Sdp                  string                 `json:"sdp" binding:"required"`
	ReservedType         string                 `json:"type" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
