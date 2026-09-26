package main

import "time"

const (
	privateFileMode    = 0o600
	privateDirMode     = 0o700
	refreshSkewSeconds = 60
	defaultPTZSpeed    = 0.5
	offerTimeout       = 15 * time.Second
	ptzStopTimeout     = 3 * time.Second
	ptzIdleTimeout     = 350 * time.Millisecond
	playerStartupDelay = 250 * time.Millisecond
	escapeKey          = 0x1b
	loopbackFirstOctet = 127
)
