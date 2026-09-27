package replay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/portpowered/go-ring/pkg/ring"
)

type playbackRequestEnvelope struct {
	Method string              `json:"method"`
	Dialog string              `json:"dialog_id"`
	Body   playbackRequestBody `json:"body"`
}

type playbackRequestBody struct {
	DeviceID   int64  `json:"doorbot_id"`
	EntryPoint string `json:"entry_point"`
	SDP        string `json:"sdp"`
	Type       string `json:"type"`
}

type playbackICEEnvelope struct {
	Method string          `json:"method"`
	Dialog string          `json:"dialog_id"`
	Body   playbackICEBody `json:"body"`
}

type playbackICEBody struct {
	DeviceID   int64  `json:"doorbot_id"`
	SessionID  string `json:"session_id"`
	ICE        string `json:"ice"`
	MLineIndex uint16 `json:"mlineindex"`
}

type playbackCloseEnvelope struct {
	Method string            `json:"method"`
	Dialog string            `json:"dialog_id"`
	Body   playbackCloseBody `json:"body"`
}

type playbackCloseBody struct {
	DeviceID  int64  `json:"doorbot_id"`
	SessionID string `json:"session_id"`
}

// The recorded playback envelope is replayed with fresh peer-generated SDP and
// ICE credentials. Captured credentials cannot establish a new media session.
func TestPlaybackReplayConnectsPeersAndReceivesMedia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverResult := make(chan error, 1)
	conn := openRecordedPeer(t, func(socket *websocket.Conn) {
		serverResult <- runPlaybackPeer(t, ctx, socket)
	})
	defer func() { _ = conn.Close() }()

	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if _, err = peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	packet := make(chan struct{}, 1)
	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}
		if _, _, readErr := track.ReadRTP(); readErr == nil {
			select {
			case packet <- struct{}{}:
			default:
			}
		}
	})
	localICE := make(chan webrtc.ICECandidateInit, 32)
	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			localICE <- candidate.ToJSON()
		}
	})
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	clientGathered := webrtc.GatheringCompletePromise(peer)
	if err = peer.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-clientGathered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var session *ring.PlaybackSession
	t.Run("SDP negotiation", func(t *testing.T) {
		session, err = conn.StartPlayback(ctx, ring.StartPlaybackRequest{DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: withoutSDPCandidates(peer.LocalDescription().SDP)}})
		if err != nil {
			t.Fatal(err)
		}
		if session.Answer().Type != ring.SDPTypeAnswer {
			t.Fatalf("answer type = %q", session.Answer().Type)
		}
		if err = peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: session.Answer().SDP}); err != nil {
			t.Fatal(err)
		}
	})
	if session == nil {
		return
	}
	defer func() { _ = session.Close() }()
	t.Run("remote ICE", func(t *testing.T) {
		event, receiveErr := session.Receive(ctx)
		if receiveErr != nil {
			t.Fatal(receiveErr)
		}
		if event.Method != "ice" {
			t.Fatalf("event method = %q", event.Method)
		}
		var body struct {
			ICE        string `json:"ice"`
			MLineIndex uint16 `json:"mlineindex"`
		}
		if err := json.Unmarshal(event.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.ICE == "" {
			t.Fatal("empty remote candidate")
		}
		if err := peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: body.ICE, SDPMLineIndex: &body.MLineIndex}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("local ICE", func(t *testing.T) {
		select {
		case candidate := <-localICE:
			if candidate.SDPMLineIndex == nil {
				t.Fatal("candidate has no m-line index")
			}
			if err := session.SendICE(ctx, ring.ICECandidateRequest{Candidate: candidate.Candidate, MLineIndex: int(*candidate.SDPMLineIndex)}); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	})
	t.Run("media packet", func(t *testing.T) {
		select {
		case <-packet:
		case <-ctx.Done():
			t.Fatal("no playback RTP packet received: ", ctx.Err())
		}
	})
	t.Run("close", func(t *testing.T) {
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-serverResult:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	})
}

func runPlaybackPeer(t *testing.T, ctx context.Context, socket *websocket.Conn) error {
	var request playbackRequestEnvelope
	if err := socket.ReadJSON(&request); err != nil {
		return err
	}
	if request.Method != "playback" || request.Dialog == "" || request.Body.DeviceID != 1000 || request.Body.EntryPoint != "timeline" || request.Body.Type != "cloud" || request.Body.SDP == "" {
		return fmt.Errorf("playback request differs from recorded envelope: %+v", request)
	}
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer func() { _ = peer.Close() }()
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video", "playback")
	if err != nil {
		return err
	}
	if _, err = peer.AddTrack(track); err != nil {
		return err
	}
	var once sync.Once
	mediaDone := make(chan struct{})
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state != webrtc.PeerConnectionStateConnected {
			return
		}
		once.Do(func() {
			go func() {
				ticker := time.NewTicker(30 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-mediaDone:
						return
					case <-ticker.C:
						_ = track.WriteSample(media.Sample{Data: []byte{0x10, 0x00, 0x00}, Duration: 30 * time.Millisecond})
					}
				}
			}()
		})
	})
	defer close(mediaDone)
	if err = peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: request.Body.SDP}); err != nil {
		return err
	}
	remoteICE := make(chan webrtc.ICECandidateInit, 32)
	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			remoteICE <- candidate.ToJSON()
		}
	})
	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		return err
	}
	serverGathered := webrtc.GatheringCompletePromise(peer)
	if err = peer.SetLocalDescription(answer); err != nil {
		return err
	}
	select {
	case <-serverGathered:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Preserve the captured SDP response fields while replacing only the peer
	// credentials and media sections with this test peer's answer.
	answerBody := capturedSignalFrame(t, "server_to_client", "dialog-2", "sdp")
	answerBody["sdp"] = withoutSDPCandidates(peer.LocalDescription().SDP)
	if err = socket.WriteJSON(map[string]any{"method": "sdp", "dialog_id": request.Dialog, "riid": "route-2", "body": answerBody}); err != nil {
		return err
	}
	select {
	case candidate := <-remoteICE:
		if candidate.SDPMLineIndex == nil {
			return fmt.Errorf("remote candidate has no m-line index")
		}
		iceBody := capturedSignalFrame(t, "server_to_client", "dialog-2", "ice")
		iceBody["ice"] = candidate.Candidate
		iceBody["mlineindex"] = *candidate.SDPMLineIndex
		if err = socket.WriteJSON(map[string]any{"method": "ice", "dialog_id": request.Dialog, "riid": "route-2", "body": iceBody}); err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	var localCandidate playbackICEEnvelope
	if err = socket.ReadJSON(&localCandidate); err != nil {
		return err
	}
	if localCandidate.Method != "ice" || localCandidate.Dialog != request.Dialog || localCandidate.Body.DeviceID != 1000 || localCandidate.Body.SessionID != answerBody["session_id"] || localCandidate.Body.ICE == "" {
		return fmt.Errorf("local ICE differs from recorded envelope: %+v", localCandidate)
	}
	if err = peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: localCandidate.Body.ICE, SDPMLineIndex: &localCandidate.Body.MLineIndex}); err != nil {
		return err
	}
	var closed playbackCloseEnvelope
	if err = socket.ReadJSON(&closed); err != nil {
		return err
	}
	if closed.Method != "close" || closed.Dialog != request.Dialog || closed.Body.DeviceID != 1000 || closed.Body.SessionID != answerBody["session_id"] {
		return fmt.Errorf("playback close differs from recorded envelope: %+v", closed)
	}
	return nil
}

func withoutSDPCandidates(sdp string) string {
	lines := strings.Split(sdp, "\r\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "a=candidate:") && line != "a=end-of-candidates" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\r\n")
}
