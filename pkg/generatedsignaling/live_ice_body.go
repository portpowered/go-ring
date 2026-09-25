package generatedsignaling

type LiveIceBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	Ice                  string                 `json:"ice" binding:"required"`
	Mid                  string                 `json:"mid" binding:"required"`
	Mlineindex           int                    `json:"mlineindex" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
