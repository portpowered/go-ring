package generatedsignaling

type IceCandidateBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	SessionId            string                 `json:"session_id" binding:"required"`
	Ice                  string                 `json:"ice" binding:"required"`
	Mlineindex           int                    `json:"mlineindex" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
