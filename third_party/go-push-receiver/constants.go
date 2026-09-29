/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import "github.com/portpowered/go-ring/internal/protocol"

// GCM / FCM constants.
const (
	fcmLegacyEndpoint = "https://fcm.googleapis.com/fcm/send/%s"
	fcmV1Endpoint     = "https://fcm.googleapis.com/v1/projects/%s/messages:send"

	fcmServerKey = "BDOU99-h67HcA6JeFXHbSNMu7e2yNNu3RzoMj8TM4W88jITfq7ZmPvIM1Iv-4_l2LxQcYwhqby2xGpWwzjfAnG4"

	mcsDomain     = protocol.MCSDomain
	chromeVersion = "63.0.3234.0"
	fcmVersion    = protocol.MCSVersion

	// MCS packet lengths.

	// versionPacketLen is the byte length of a version packet.
	versionPacketLen = protocol.MCSVersionPacketBytes
	// tagPacketLen is the byte length of a tag packet.
	tagPacketLen     = protocol.MCSTagPacketBytes
	sizePacketLenMin = 1
	sizePacketLenMax = 5
)

// Default values.
const (
	// defaultDialTimeout is the default dial timeout in seconds.
	defaultDialTimeout = 30

	// defaultKeepAlive is the default keep-alive duration in minutes.
	defaultKeepAlive = 1

	// defaultBackoffBase is the default backoff base in seconds.
	defaultBackoffBase = 5

	// defaultBackoffMax is the maximum backoff in seconds.
	defaultBackoffMax = 15 * 60

	// defaultHeartbeatPeriod is the default heartbeat period in minutes.
	defaultHeartbeatPeriod = 10
)
