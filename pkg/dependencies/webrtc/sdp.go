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

	err := description.UnmarshalString(raw)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("invalid SDP syntax", err)
	}

	if len(description.MediaDescriptions) == 0 {
		return nil, ringerrors.NewBadRequestError("SDP has no media sections", nil)
	}

	sessionDirectionErr := validateSessionDirection(description.Attributes)
	if sessionDirectionErr != nil {
		return nil, sessionDirectionErr
	}

	mids, err := validateMediaSections(description.MediaDescriptions)
	if err != nil {
		return nil, err
	}

	bundleErr := validateBundleGroups(description.Attributes, mids)
	if bundleErr != nil {
		return nil, bundleErr
	}

	return &description, nil
}

func validateSessionDirection(attributes []sdp.Attribute) error {
	directions := 0

	for _, attribute := range attributes {
		if isDirection(attribute.Key) {
			directions++
		}
	}

	if directions > 1 {
		return ringerrors.NewBadRequestError("SDP has conflicting session directions", nil)
	}

	return nil
}

func validateMediaSections(mediaSections []*sdp.MediaDescription) (map[string]bool, error) {
	mids := make(map[string]bool, len(mediaSections))

	for _, media := range mediaSections {
		mid, ok := media.Attribute("mid")
		if !ok || mid == "" || mids[mid] {
			return nil, ringerrors.NewBadRequestError("SDP requires unique media IDs", nil)
		}

		mids[mid] = true

		attributesErr := validateMediaAttributes(media.Attributes)
		if attributesErr != nil {
			return nil, attributesErr
		}
	}

	return mids, nil
}

func validateMediaAttributes(attributes []sdp.Attribute) error {
	directions, mediaIDs := 0, 0

	for _, attribute := range attributes {
		if attribute.Key == "mid" {
			mediaIDs++
		}

		if isDirection(attribute.Key) {
			directions++
		}
	}

	if mediaIDs != 1 {
		return ringerrors.NewBadRequestError("SDP section must have exactly one media ID", nil)
	}

	if directions > 1 {
		return ringerrors.NewBadRequestError("SDP has conflicting media directions", nil)
	}

	return nil
}

func validateBundleGroups(attributes []sdp.Attribute, mids map[string]bool) error {
	for _, attribute := range attributes {
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
				return ringerrors.NewBadRequestError("SDP bundle references invalid media ID", nil)
			}

			seen[mid] = true
		}
	}

	return nil
}

func isDirection(attribute string) bool {
	switch attribute {
	case protocol.SDPSendRecv, protocol.SDPSendOnly, protocol.SDPRecvOnly, protocol.SDPInactive:
		return true
	default:
		return false
	}
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
	offerDescription, err := ParseSDP(offer)
	if err != nil {
		return "", err
	}

	answerDescription, err := ParseSDP(answer)
	if err != nil {
		return "", err
	}

	if len(offerDescription.MediaDescriptions) != len(answerDescription.MediaDescriptions) {
		return "", ringerrors.NewBadRequestError("SDP answer media count differs", nil)
	}

	changed, err := normalizeAnswerMediaSections(offerDescription, answerDescription)
	if err != nil {
		return "", err
	}

	if !changed {
		return answer, nil
	}

	encoded, err := answerDescription.Marshal()
	if err != nil {
		return "", ringerrors.NewInternalServerError("cannot encode SDP answer", err)
	}

	return string(encoded), nil
}

func normalizeAnswerMediaSections(
	offer *sdp.SessionDescription,
	answer *sdp.SessionDescription,
) (bool, error) {
	changed := false

	for index, answerMedia := range answer.MediaDescriptions {
		offerMedia := offer.MediaDescriptions[index]

		if !sameMediaIdentity(offerMedia, answerMedia) {
			return false, ringerrors.NewBadRequestError("SDP answer media order differs", nil)
		}

		if answerMedia.MediaName.Port.Value == 0 {
			continue
		}

		if offerMedia.MediaName.Port.Value == 0 {
			return false, ringerrors.NewBadRequestError("SDP answer reactivates a rejected offer section", nil)
		}

		changed = normalizeRecvOnlyAnswer(offer, answer, offerMedia, answerMedia) || changed

		if !answerDirectionAllowed(offer, answer, offerMedia, answerMedia) {
			return false, ringerrors.NewBadRequestError("SDP answer direction incompatible with offer", nil)
		}
	}

	return changed, nil
}

func sameMediaIdentity(offer, answer *sdp.MediaDescription) bool {
	offeredMID, _ := offer.Attribute("mid")
	answerMID, _ := answer.Attribute("mid")

	return answerMID == offeredMID && answer.MediaName.Media == offer.MediaName.Media
}

func normalizeRecvOnlyAnswer(
	offerDescription *sdp.SessionDescription,
	answerDescription *sdp.SessionDescription,
	offer, answer *sdp.MediaDescription,
) bool {
	if direction(offerDescription, offer) != protocol.SDPRecvOnly ||
		direction(answerDescription, answer) != protocol.SDPSendRecv {
		return false
	}

	found := false

	for index := range answer.Attributes {
		if answer.Attributes[index].Key == protocol.SDPSendRecv {
			answer.Attributes[index].Key = protocol.SDPSendOnly
			found = true
		}
	}

	if !found {
		answer.Attributes = append(answer.Attributes, sdp.NewPropertyAttribute(protocol.SDPSendOnly))
	}

	return true
}

func answerDirectionAllowed(
	offerDescription *sdp.SessionDescription,
	answerDescription *sdp.SessionDescription,
	offer, answer *sdp.MediaDescription,
) bool {
	offerDirection := direction(offerDescription, offer)
	answerDirection := direction(answerDescription, answer)

	if offerDirection == protocol.SDPRecvOnly {
		return answerDirection == protocol.SDPSendOnly || answerDirection == protocol.SDPInactive
	}

	if offerDirection == protocol.SDPSendOnly {
		return answerDirection == protocol.SDPRecvOnly || answerDirection == protocol.SDPInactive
	}

	return offerDirection != protocol.SDPInactive || answerDirection == protocol.SDPInactive
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
