package protocol

// EndpointSet contains per-client service origins. Empty values inherit the
// selected region's evidenced defaults; URLs must be absolute HTTP(S)/WS(S).
type EndpointSet struct {
	OAuthBaseURL     string
	APIBaseURL       string
	SolutionsBaseURL string
	SignalingURL     string
}

// Profile returns the verified endpoint defaults for a region. Ring's public
// API and OAuth hosts are established, while only the US Solutions bootstrap
// origin is evidenced. EU and FE callers must provide SolutionsBaseURL.
func Profile(region string) EndpointSet {
	p := EndpointSet{OAuthBaseURL: OAuthBaseURL, APIBaseURL: APIBaseURL, SignalingURL: SignalingURL}
	if region == "US" {
		p.SolutionsBaseURL = USSolutionsBaseURL
	}
	return p
}
