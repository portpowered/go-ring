/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"crypto/rand"
	"math/big"
	"time"
)

// Backoff with jitter sleep to prevent overloaded conditions during intervals
// https://www.awsarchitectureblog.com/2015/03/backoff.html
type Backoff struct {
	attempts int
	base     int64
	max      int64
}

// NewBackoff creates Backoff instance.
func NewBackoff(base time.Duration, maxDuration time.Duration) *Backoff {
	return &Backoff{
		attempts: 0,
		base:     int64(base),
		max:      int64(maxDuration),
	}
}

func (b *Backoff) duration() time.Duration {
	const (
		maxExponent = 63
		maxInt64    = int64(1<<63 - 1)
	)

	if b.attempts < maxExponent {
		b.attempts++
	}

	if b.base <= 0 || b.max <= 0 {
		return 0
	}

	bound := b.base
	for range b.attempts {
		if bound > maxInt64/2 {
			bound = maxInt64

			break
		}

		bound *= 2
	}

	randomDuration, err := rand.Int(rand.Reader, big.NewInt(bound))
	if err != nil {
		return 0
	}

	duration := randomDuration.Int64()

	if duration > b.max {
		return time.Duration(b.max)
	}

	return time.Duration(duration)
}

func (b *Backoff) reset() {
	b.attempts = 0
}
