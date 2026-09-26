// Package webrtc validates captured SDP and ICE media identities.
package webrtc

import (
	"strings"

	"github.com/pion/sdp/v3"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

const maxSDPBytes = 1 << 20

// ParseSDP validates the media identities used to route trickled ICE. It does not
// certify codec interoperability or replace a peer connection's SDP validation.
func ParseSDP(raw string) (*sdp.SessionDescription, error) {
	if len(raw) > maxSDPBytes {
		return nil, ringerrors.NewBadRequestError("SDP exceeds size limit", nil)
	}
	var description sdp.SessionDescription
	// The recorded Android offers omit a final line ending. Pion's parser
	// requires one; normalize only this framing detail before parsing.
	if !strings.HasSuffix(raw, "\n") {
		raw += "\r\n"
	}
	if err := description.UnmarshalString(raw); err != nil {
		return nil, ringerrors.NewBadRequestError("invalid SDP syntax", err)
	}
	if len(description.MediaDescriptions) == 0 {
		return nil, ringerrors.NewBadRequestError("SDP has no media sections", nil)
	}
	sessionDirections := 0
	for _, attribute := range description.Attributes {
		switch attribute.Key {
		case protocol.SDPSendRecv, protocol.SDPSendOnly, protocol.SDPRecvOnly, protocol.SDPInactive:
			sessionDirections++
		}
	}
	if sessionDirections > 1 {
		return nil, ringerrors.NewBadRequestError("SDP has conflicting session directions", nil)
	}
	mids := make(map[string]bool)
	for _, media := range description.MediaDescriptions {
		mid, ok := media.Attribute("mid")
		if !ok || mid == "" || mids[mid] {
			return nil, ringerrors.NewBadRequestError("SDP requires unique media IDs", nil)
		}
		mids[mid] = true
		count, midCount := 0, 0
		for _, attribute := range media.Attributes {
			if attribute.Key == "mid" {
				midCount++
			}
			switch attribute.Key {
			case protocol.SDPSendRecv, protocol.SDPSendOnly, protocol.SDPRecvOnly, protocol.SDPInactive:
				count++
			}
		}
		if midCount != 1 {
			return nil, ringerrors.NewBadRequestError("SDP section must have exactly one media ID", nil)
		}
		if count > 1 {
			return nil, ringerrors.NewBadRequestError("SDP has conflicting media directions", nil)
		}
	}
	for _, attribute := range description.Attributes {
		if attribute.Key != "group" {
			continue
		}
		fields := strings.Fields(attribute.Value)
		if len(fields) == 0 || fields[0] != "BUNDLE" {
			continue
		}
		seen := make(map[string]bool)
		for _, mid := range fields[1:] {
			if !mids[mid] || seen[mid] {
				return nil, ringerrors.NewBadRequestError("SDP bundle references invalid media ID", nil)
			}
			seen[mid] = true
		}
	}
	return &description, nil
}

func direction(description *sdp.SessionDescription, media *sdp.MediaDescription) string {
	for _, attributes := range [][]sdp.Attribute{media.Attributes, description.Attributes} {
		for _, attribute := range attributes {
			switch attribute.Key {
			case protocol.SDPSendRecv, protocol.SDPSendOnly, protocol.SDPRecvOnly, protocol.SDPInactive:
				return attribute.Key
			}
		}
	}
	return protocol.SDPSendRecv
}

// NormalizeAnswer applies the observed recvonly/sendrecv workaround by MID,
// never by media kind. Other attributes, including vendor extensions, survive.
func NormalizeAnswer(offer, answer string) (string, error) {
	o, err := ParseSDP(offer)
	if err != nil {
		return "", err
	}
	a, err := ParseSDP(answer)
	if err != nil {
		return "", err
	}
	if len(o.MediaDescriptions) != len(a.MediaDescriptions) {
		return "", ringerrors.NewBadRequestError("SDP answer media count differs", nil)
	}
	changed := false
	for i, media := range a.MediaDescriptions {
		original := o.MediaDescriptions[i]
		mid, _ := media.Attribute("mid")
		offeredMID, _ := original.Attribute("mid")
		if mid != offeredMID || media.MediaName.Media != original.MediaName.Media {
			return "", ringerrors.NewBadRequestError("SDP answer media order differs", nil)
		}
		if media.MediaName.Port.Value == 0 {
			continue
		}
		if original.MediaName.Port.Value == 0 {
			return "", ringerrors.NewBadRequestError("SDP answer reactivates a rejected offer section", nil)
		}
		if direction(o, original) == protocol.SDPRecvOnly && direction(a, media) == protocol.SDPSendRecv {
			found := false
			for j := range media.Attributes {
				if media.Attributes[j].Key == protocol.SDPSendRecv {
					media.Attributes[j].Key = protocol.SDPSendOnly
					found = true
				}
			}
			if !found {
				media.Attributes = append(media.Attributes, sdp.NewPropertyAttribute(protocol.SDPSendOnly))
			}
			changed = true
		}
		// RFC 3264 section 6.1, after the capture-backed recvonly workaround.
		offerDirection, answerDirection := direction(o, original), direction(a, media)
		invalid := offerDirection == protocol.SDPRecvOnly && answerDirection != protocol.SDPSendOnly && answerDirection != protocol.SDPInactive
		invalid = invalid || offerDirection == protocol.SDPSendOnly && answerDirection != protocol.SDPRecvOnly && answerDirection != protocol.SDPInactive
		invalid = invalid || offerDirection == protocol.SDPInactive && answerDirection != protocol.SDPInactive
		if invalid {
			return "", ringerrors.NewBadRequestError("SDP answer direction incompatible with offer", nil)
		}
	}
	if !changed {
		return answer, nil
	}
	encoded, err := a.Marshal()
	if err != nil {
		return "", ringerrors.NewInternalServerError("cannot encode SDP answer", err)
	}
	return string(encoded), nil
}

// ValidateICE checks that the candidate's optional MID and index refer to the
// same media section. Candidate grammar remains the peer implementation's job.
func ValidateICE(description *sdp.SessionDescription, mid string, index int) error {
	if description == nil || index < 0 || index >= len(description.MediaDescriptions) {
		return ringerrors.NewBadRequestError("ICE media index out of range", nil)
	}
	expected, _ := description.MediaDescriptions[index].Attribute("mid")
	if mid != "" && mid != expected {
		return ringerrors.NewBadRequestError("ICE media ID and index differ", nil)
	}
	return nil
}
