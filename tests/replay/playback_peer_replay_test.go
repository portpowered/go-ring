package replay_test

import (
	"context"
	"encoding/json"
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
	t.Cleanup(cancel)

	clientCloseReturned := make(chan struct{})

	var clientCloseReturnedOnce sync.Once

	releaseClientClose := func() {
		clientCloseReturnedOnce.Do(func() { close(clientCloseReturned) })
	}

	serverResult := make(chan error, 1)
	conn := openRecordedPeer(t, func(socket *websocket.Conn) {
		serverResult <- runPlaybackPeer(t, ctx, socket, clientCloseReturned)
	})

	t.Cleanup(releaseClientClose)
	t.Cleanup(func() { _ = conn.Close() })

	peer, packet, localICE := newPlaybackClientPeer(t)

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}

	clientGathered := webrtc.GatheringCompletePromise(peer)

	{
		err = peer.SetLocalDescription(offer)
		if err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-clientGathered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	var session *ring.PlaybackSession

	t.Run("SDP negotiation", func(t *testing.T) {
		session, err = conn.StartPlayback(
			ctx,
			ring.StartPlaybackRequest{
				DeviceID: "1000",
				Offer: ring.SessionDescription{
					Type: ring.SDPTypeOffer,
					SDP:  withoutSDPCandidates(peer.LocalDescription().SDP),
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}

		if session.Answer().Type != ring.SDPTypeAnswer {
			t.Fatalf("answer type = %q", session.Answer().Type)
		}

		err = peer.SetRemoteDescription(
			webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: session.Answer().SDP},
		)
		if err != nil {
			t.Fatal(err)
		}
	})

	if session == nil {
		return
	}

	t.Cleanup(func() { _ = session.Close() })

	t.Run("remote ICE", func(t *testing.T) {
		assertPlaybackRemoteICE(t, ctx, session, peer)
	})
	t.Run("local ICE", func(t *testing.T) {
		assertPlaybackLocalICE(t, ctx, session, localICE)
	})
	t.Run("media packet", func(t *testing.T) {
		select {
		case <-packet:
		case <-ctx.Done():
			t.Fatal("no playback RTP packet received: ", ctx.Err())
		}
	})
	t.Run("close", func(t *testing.T) {
		//nolint:contextcheck // PlaybackSession.Close has no context parameter; the replay's wait uses ctx below.
		err := session.Close()
		if err != nil {
			t.Fatal(err)
		}

		releaseClientClose()

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

func assertPlaybackRemoteICE(
	t *testing.T,
	ctx context.Context,
	session *ring.PlaybackSession,
	peer *webrtc.PeerConnection,
) {
	t.Helper()

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

	err := json.Unmarshal(event.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	if body.ICE == "" {
		t.Fatal("empty remote candidate")
	}

	err = peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: body.ICE, SDPMLineIndex: &body.MLineIndex})
	if err != nil {
		t.Fatal(err)
	}
}

func assertPlaybackLocalICE(
	t *testing.T,
	ctx context.Context,
	session *ring.PlaybackSession,
	localICE <-chan webrtc.ICECandidateInit,
) {
	t.Helper()

	select {
	case candidate := <-localICE:
		if candidate.SDPMLineIndex == nil {
			t.Fatal("candidate has no m-line index")
		}

		err := session.SendICE(
			ctx,
			ring.ICECandidateRequest{Candidate: candidate.Candidate, MLineIndex: int(*candidate.SDPMLineIndex)},
		)
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func newPlaybackClientPeer(t *testing.T) (*webrtc.PeerConnection, chan struct{}, chan webrtc.ICECandidateInit) {
	t.Helper()

	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = peer.Close() })

	{
		_, err = peer.AddTransceiverFromKind(
			webrtc.RTPCodecTypeVideo,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	packet := make(chan struct{}, 1)

	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}

		{
			_, _, readErr := track.ReadRTP()
			if readErr == nil {
				select {
				case packet <- struct{}{}:
				default:
				}
			}
		}
	})

	localICE := make(chan webrtc.ICECandidateInit, 32)

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			localICE <- candidate.ToJSON()
		}
	})

	return peer, packet, localICE
}

func runPlaybackPeer(
	t *testing.T,
	ctx context.Context,
	socket *websocket.Conn,
	clientCloseReturned <-chan struct{},
) error {
	t.Helper()

	var request playbackRequestEnvelope
	{
		err := socket.ReadJSON(&request)
		if err != nil {
			return wrapReplayTestError("read playback request", err)
		}
	}

	if request.Method != playbackWireToken || request.Dialog == "" || request.Body.DeviceID != 1000 ||
		request.Body.EntryPoint != playbackTimelineEntryPoint ||
		request.Body.Type != "cloud" ||
		request.Body.SDP == "" {
		return testReplayErrorf("playback request differs from recorded envelope: %+v", request)
	}

	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return wrapReplayTestError("create playback peer", err)
	}

	defer func() { _ = peer.Close() }()

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video",
		playbackWireToken,
	)
	if err != nil {
		return wrapReplayTestError("create playback video track", err)
	}

	{
		_, err = peer.AddTrack(track)
		if err != nil {
			return wrapReplayTestError("add playback video track", err)
		}
	}

	stopMedia := startPlaybackMedia(peer, track)
	defer stopMedia()

	{
		err = peer.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  request.Body.SDP,
		})
		if err != nil {
			return wrapReplayTestError("set playback remote description", err)
		}
	}

	remoteICE := make(chan webrtc.ICECandidateInit, 32)

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			remoteICE <- candidate.ToJSON()
		}
	})

	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		return wrapReplayTestError("create playback answer", err)
	}

	serverGathered := webrtc.GatheringCompletePromise(peer)

	{
		err = peer.SetLocalDescription(answer)
		if err != nil {
			return wrapReplayTestError("set playback local description", err)
		}
	}

	select {
	case <-serverGathered:
	case <-ctx.Done():
		return wrapReplayTestError("wait for playback ICE gathering", ctx.Err())
	}
	// Preserve the captured SDP response fields while replacing only the peer
	// credentials and media sections with this test peer's answer.
	answerBody := capturedSignalFrame(t, "server_to_client", "dialog-2", "sdp")

	answerBody["sdp"] = withoutSDPCandidates(peer.LocalDescription().SDP)

	{
		err = socket.WriteJSON(map[string]any{
			"method":    "sdp",
			"dialog_id": request.Dialog,
			"riid":      "route-2",
			"body":      answerBody,
		})
		if err != nil {
			return wrapReplayTestError("write playback SDP answer", err)
		}
	}

	err = sendPlaybackRemoteICE(t, ctx, socket, request.Dialog, remoteICE)
	if err != nil {
		return err
	}

	err = receivePlaybackLocalICE(socket, peer, request.Dialog, answerBody["session_id"])
	if err != nil {
		return err
	}

	err = readPlaybackClose(socket, request.Dialog, answerBody["session_id"])
	if err != nil {
		return err
	}

	return waitForPlaybackCloseSend(ctx, clientCloseReturned)
}

func waitForPlaybackCloseSend(ctx context.Context, clientCloseReturned <-chan struct{}) error {
	select {
	case <-clientCloseReturned:
		return nil
	case <-ctx.Done():
		return wrapReplayTestError("wait for playback close send to complete", ctx.Err())
	}
}

func receivePlaybackLocalICE(
	socket *websocket.Conn,
	peer *webrtc.PeerConnection,
	dialog string,
	sessionID any,
) error {
	var localCandidate playbackICEEnvelope

	err := socket.ReadJSON(&localCandidate)
	if err != nil {
		return wrapReplayTestError("read local playback ICE candidate", err)
	}

	if localCandidate.Method != "ice" || localCandidate.Dialog != dialog ||
		localCandidate.Body.DeviceID != 1000 ||
		localCandidate.Body.SessionID != sessionID ||
		localCandidate.Body.ICE == "" {
		return testReplayErrorf("local ICE differs from recorded envelope: %+v", localCandidate)
	}

	err = peer.AddICECandidate(webrtc.ICECandidateInit{
		Candidate:     localCandidate.Body.ICE,
		SDPMLineIndex: &localCandidate.Body.MLineIndex,
	})
	if err != nil {
		return wrapReplayTestError("add local playback ICE candidate", err)
	}

	return nil
}

func readPlaybackClose(socket *websocket.Conn, dialog string, sessionID any) error {
	var closed playbackCloseEnvelope

	err := socket.ReadJSON(&closed)
	if err != nil {
		return wrapReplayTestError("read playback close frame", err)
	}

	if closed.Method != "close" || closed.Dialog != dialog || closed.Body.DeviceID != 1000 ||
		closed.Body.SessionID != sessionID {
		return testReplayErrorf("playback close differs from recorded envelope: %+v", closed)
	}

	return nil
}

func sendPlaybackRemoteICE(
	t *testing.T,
	ctx context.Context,
	socket *websocket.Conn,
	dialog string,
	remoteICE <-chan webrtc.ICECandidateInit,
) error {
	t.Helper()

	select {
	case candidate := <-remoteICE:
		if candidate.SDPMLineIndex == nil {
			return testReplayError("remote candidate has no m-line index")
		}

		iceBody := capturedSignalFrame(t, "server_to_client", "dialog-2", "ice")
		iceBody["ice"] = candidate.Candidate
		iceBody["mlineindex"] = *candidate.SDPMLineIndex

		err := socket.WriteJSON(
			map[string]any{"method": "ice", "dialog_id": dialog, "riid": "route-2", "body": iceBody},
		)
		if err != nil {
			return wrapReplayTestError("write playback ICE candidate", err)
		}
	case <-ctx.Done():
		return wrapReplayTestError("wait for remote playback ICE candidate", ctx.Err())
	}

	return nil
}

func startPlaybackMedia(peer *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample) func() {
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
						_ = track.WriteSample(
							media.Sample{Data: []byte{0x10, 0x00, 0x00}, Duration: 30 * time.Millisecond},
						)
					}
				}
			}()
		})
	})

	return func() { close(mediaDone) }
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
