// Package signaling implements wire-level session validation and lifecycle rules.
package signaling

import (
	"fmt"
	"strings"

	"github.com/pion/sdp/v3"
)

// ParseSDP validates the media identities used to route trickled ICE. It does not
// certify codec interoperability or replace a peer connection's SDP validation.
func ParseSDP(raw string) (*sdp.SessionDescription, error) {
	if len(raw) > MaxMessageBytes {
		return nil, fmt.Errorf("SDP exceeds size limit")
	}
	var description sdp.SessionDescription
	// The recorded Android offers omit a final line ending. Pion's parser
	// requires one; normalize only this framing detail before parsing.
	if !strings.HasSuffix(raw, "\n") {
		raw += "\r\n"
	}
	if err := description.UnmarshalString(raw); err != nil {
		return nil, fmt.Errorf("invalid SDP syntax")
	}
	if len(description.MediaDescriptions) == 0 {
		return nil, fmt.Errorf("SDP has no media sections")
	}
	sessionDirections := 0
	for _, attribute := range description.Attributes {
		switch attribute.Key {
		case "sendrecv", "sendonly", "recvonly", "inactive":
			sessionDirections++
		}
	}
	if sessionDirections > 1 {
		return nil, fmt.Errorf("SDP has conflicting session directions")
	}
	mids := make(map[string]bool)
	for _, media := range description.MediaDescriptions {
		mid, ok := media.Attribute("mid")
		if !ok || mid == "" || mids[mid] {
			return nil, fmt.Errorf("SDP requires unique media IDs")
		}
		mids[mid] = true
		count := 0
		for _, attribute := range media.Attributes {
			switch attribute.Key {
			case "sendrecv", "sendonly", "recvonly", "inactive":
				count++
			}
		}
		if count > 1 {
			return nil, fmt.Errorf("SDP has conflicting media directions")
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
				return nil, fmt.Errorf("SDP bundle references invalid media ID")
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
			case "sendrecv", "sendonly", "recvonly", "inactive":
				return attribute.Key
			}
		}
	}
	return "sendrecv"
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
		return "", fmt.Errorf("SDP answer media count differs")
	}
	changed := false
	for i, media := range a.MediaDescriptions {
		original := o.MediaDescriptions[i]
		mid, _ := media.Attribute("mid")
		offeredMID, _ := original.Attribute("mid")
		if mid != offeredMID || media.MediaName.Media != original.MediaName.Media {
			return "", fmt.Errorf("SDP answer media order differs")
		}
		if media.MediaName.Port.Value == 0 {
			continue
		}
		if original.MediaName.Port.Value == 0 {
			return "", fmt.Errorf("SDP answer reactivates a rejected offer section")
		}
		if direction(o, original) == "recvonly" && direction(a, media) == "sendrecv" {
			found := false
			for j := range media.Attributes {
				if media.Attributes[j].Key == "sendrecv" {
					media.Attributes[j].Key = "sendonly"
					found = true
				}
			}
			if !found {
				media.Attributes = append(media.Attributes, sdp.NewPropertyAttribute("sendonly"))
			}
			changed = true
		}
		// RFC 3264 section 6.1, after the capture-backed recvonly workaround.
		offerDirection, answerDirection := direction(o, original), direction(a, media)
		invalid := offerDirection == "recvonly" && answerDirection != "sendonly" && answerDirection != "inactive"
		invalid = invalid || offerDirection == "sendonly" && answerDirection != "recvonly" && answerDirection != "inactive"
		invalid = invalid || offerDirection == "inactive" && answerDirection != "inactive"
		if invalid {
			return "", fmt.Errorf("SDP answer direction incompatible with offer")
		}
	}
	if !changed {
		return answer, nil
	}
	encoded, err := a.Marshal()
	if err != nil {
		return "", fmt.Errorf("cannot encode SDP answer")
	}
	return string(encoded), nil
}

// ValidateICE checks that the candidate's optional MID and index refer to the
// same media section. Candidate grammar remains the peer implementation's job.
func ValidateICE(description *sdp.SessionDescription, mid string, index int) error {
	if description == nil || index < 0 || index >= len(description.MediaDescriptions) {
		return fmt.Errorf("ICE media index out of range")
	}
	expected, _ := description.MediaDescriptions[index].Attribute("mid")
	if mid != "" && mid != expected {
		return fmt.Errorf("ICE media ID and index differ")
	}
	return nil
}
