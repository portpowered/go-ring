package rest

import "github.com/portpowered/go-ring/internal/generatedhttp"

// deviceModel is sent in OAuth, session registration, and push registration.
const deviceModel = generatedhttp.GoRing

const (
	maxCSRFSearchDepth = 8
)
