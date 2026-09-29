package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/portpowered/go-ring/pkg/ring"
	"golang.org/x/term"
)

const ffplayCommand = "ffplay"

type viewOptions struct {
	player     string
	iceFile    string
	recordRTP  string
	continuous bool
	speed      float64
	debug      bool
}

func viewCommand(parent context.Context, store tokenStore, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return commandError("view requires a device ID")
	}

	flags := flag.NewFlagSet("view", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	player := flags.String("player", ffplayCommand, "ffplay or none")
	iceFile := flags.String("ice-servers", "", "JSON array of ICE servers")
	continuous := flags.Bool("continuous", false, "continuous PTZ with inactivity stop")
	speed := flags.Float64("speed", defaultPTZSpeed, "continuous PTZ speed from 0 to 1")
	debug := flags.Bool("debug", false, "show connection and control diagnostics")

	recordRTP := flags.String("record-rtp", "", "record incoming video RTP packets for offline diagnosis")

	err := flags.Parse(args[1:])
	if err != nil {
		return wrapCommandError("parse view flags", err)
	}

	if flags.NArg() != 0 || (*player != "none" && *player != ffplayCommand) || *speed <= 0 || *speed > 1 {
		return commandError("invalid view options")
	}

	interrupts := make(chan os.Signal, 1)

	signal.Notify(interrupts, os.Interrupt)

	defer signal.Stop(interrupts)

	return withClient(parent, store, func(client *ring.Client, auth ring.AuthContext) error {
		opts := viewOptions{
			player:     *player,
			iceFile:    *iceFile,
			recordRTP:  *recordRTP,
			continuous: *continuous,
			speed:      *speed,
			debug:      *debug,
		}

		return view(parent, client, auth, args[0], opts, in, out, interrupts)
	})
}

func view(
	parent context.Context,
	client *ring.Client,
	auth ring.AuthContext,
	deviceID string,
	opts viewOptions,
	in io.Reader,
	out io.Writer,
	interrupts <-chan os.Signal,
) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	config, err := videoConfiguration(opts.iceFile)
	if err != nil {
		return err
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return wrapCommandError("create view peer connection", err)
	}

	defer func() { _ = pc.Close() }()

	if opts.debug {
		pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
			_, _ = fmt.Fprintf(out, "ICE connection: %s\n", state)
		})
		pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
			_, _ = fmt.Fprintf(out, "Peer connection: %s\n", state)
		})
	}

	_, err = pc.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	)
	if err != nil {
		return wrapCommandError("add view video transceiver", err)
	}

	mediaErr := make(chan error, 1)
	registerViewTrackHandler(ctx, pc, opts, mediaErr, out)

	conn, session, err := startVideoSession(ctx, client, auth, deviceID, pc)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()
	defer func() { _ = session.Close() }()

	if opts.debug {
		_, _ = fmt.Fprintln(out, "Remote SDP answer applied")
	}

	events := make(chan error, 1)
	go receiveICE(ctx, session, pc, events)

	keys := make(chan rune)

	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		old, err := term.MakeRaw(int(file.Fd()))
		if err != nil {
			return wrapCommandError("configure terminal for PTZ control", err)
		}

		defer func() { _ = term.Restore(int(file.Fd()), old) }()
	}

	go readKeys(in, keys)

	_, _ = fmt.Fprintln(out, "Session active. Arrow keys move camera; Space stops; q quits.")

	return controlLoop(ctx, session, opts, keys, events, mediaErr, interrupts, out)
}

func registerViewTrackHandler(
	ctx context.Context,
	pc *webrtc.PeerConnection,
	opts viewOptions,
	mediaErr chan<- error,
	out io.Writer,
) {
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		_, _ = fmt.Fprintf(out, "Video track: %s\n", track.Codec().MimeType)

		if opts.player == ffplayCommand {
			err := playTrack(ctx, track, opts.recordRTP)
			if err != nil && ctx.Err() == nil {
				reportMediaError(mediaErr, err)
			}

			return
		}

		recordViewTrack(track, opts.recordRTP, mediaErr, out)
	})
}

func recordViewTrack(track *webrtc.TrackRemote, recordPath string, mediaErr chan<- error, out io.Writer) {
	var recording *rtpRecording

	if recordPath != "" {
		var err error

		recording, err = newRTPRecording(recordPath)
		if err != nil {
			reportMediaError(mediaErr, err)

			return
		}

		defer func() { _ = recording.Close() }()
	}

	var packets uint64

	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return
		}

		if recording != nil {
			err = recording.WritePacket(packet)
			if err != nil {
				reportMediaError(mediaErr, err)

				return
			}
		}

		packets++
		if packets == 1 {
			_, _ = fmt.Fprintln(out, "First video packet received")
		}
	}
}

func reportMediaError(mediaErr chan<- error, err error) {
	select {
	case mediaErr <- err:
	default:
	}
}

func videoConfiguration(iceFile string) (webrtc.Configuration, error) {
	config := webrtc.Configuration{}
	if iceFile == "" {
		return config, nil
	}

	data, err := os.ReadFile(iceFile) // #nosec G304 -- User-selected ICE configuration path.
	if err != nil {
		return config, wrapCommandError("read ICE server configuration", err)
	}

	err = json.Unmarshal(data, &config.ICEServers)
	if err != nil {
		return config, wrapCommandError("invalid ICE server JSON", err)
	}

	return config, nil
}

func startVideoSession(
	ctx context.Context,
	client *ring.Client,
	auth ring.AuthContext,
	deviceID string,
	pc *webrtc.PeerConnection,
) (*ring.SignalingConnection, *ring.DeviceSession, error) {
	offer, err := makeVideoOffer(ctx, pc)
	if err != nil {
		return nil, nil, err
	}

	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
	if err != nil {
		return nil, nil, wrapCommandError("open signaling connection", err)
	}

	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID:     deviceID,
		Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
		VideoEnabled: true,
		AudioEnabled: false,
		ICEMode:      ring.ICENonTrickle,
		MaxAge:       0,
	})
	if err != nil {
		_ = conn.Close()

		return nil, nil, wrapCommandError("start live view session", err)
	}

	err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: session.Answer().SDP})
	if err != nil {
		_ = session.Close()
		_ = conn.Close()

		return nil, nil, wrapCommandError("apply live view answer", err)
	}

	return conn, session, nil
}

func makeVideoOffer(ctx context.Context, pc *webrtc.PeerConnection) (string, error) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", wrapCommandError("create live view offer", err)
	}

	complete := webrtc.GatheringCompletePromise(pc)

	err = pc.SetLocalDescription(offer)
	if err != nil {
		return "", wrapCommandError("apply local live view offer", err)
	}

	timer := time.NewTimer(offerTimeout)
	defer timer.Stop()

	select {
	case <-complete:
		return pc.LocalDescription().SDP, nil
	case <-timer.C:
		return "", commandError("ICE gathering timed out")
	case <-ctx.Done():
		return "", wrapCommandError("gather ICE candidates", ctx.Err())
	}
}

func receiveICE(ctx context.Context, session *ring.DeviceSession, pc *webrtc.PeerConnection, done chan<- error) {
	for {
		event, err := session.Receive(ctx)
		if err != nil {
			done <- err

			return
		}

		if event.Method != "ice" {
			continue
		}

		var body struct {
			Candidate string `json:"ice"`
			Index     uint16 `json:"mlineindex"`
		}

		if json.Unmarshal(event.Body, &body) != nil || body.Candidate == "" {
			done <- commandError("invalid remote ICE candidate")

			return
		}

		err = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: body.Candidate, SDPMLineIndex: &body.Index})
		if err != nil {
			done <- wrapCommandError("add remote ICE candidate", err)

			return
		}
	}
}

func readKeys(in io.Reader, keys chan<- rune) {
	defer close(keys)

	reader := bufio.NewReader(in)

	for {
		key, emit, continueReading := readKey(reader)
		if !continueReading {
			return
		}

		if !emit {
			continue
		}

		keys <- key
	}
}

func readKey(reader *bufio.Reader) (rune, bool, bool) {
	value, err := reader.ReadByte()
	if err != nil {
		return 0, false, false
	}

	if value == escapeKey {
		return readEscapeKey(reader)
	}

	if value == 0 || value == 0xe0 {
		return readWindowsKey(reader)
	}

	return rune(value), true, true
}

func readEscapeKey(reader *bufio.Reader) (rune, bool, bool) {
	lead, err := reader.ReadByte()
	if err != nil {
		return 0, false, false
	}

	if lead != '[' {
		return 0, false, true
	}

	value, err := reader.ReadByte()
	if err != nil {
		return 0, false, false
	}

	return rune(value), true, true
}

func readWindowsKey(reader *bufio.Reader) (rune, bool, bool) {
	value, err := reader.ReadByte()
	if err != nil {
		return 0, false, false
	}

	switch value {
	case 'H':
		return 'A', true, true
	case 'P':
		return 'B', true, true
	case 'M':
		return 'C', true, true
	case 'K':
		return 'D', true, true
	default:
		return 0, false, true
	}
}

func controlLoop(
	ctx context.Context,
	session *ring.DeviceSession,
	opts viewOptions,
	keys <-chan rune,
	events <-chan error,
	mediaErr <-chan error,
	interrupts <-chan os.Signal,
	out io.Writer,
) error {
	control := &ptzController{
		session:       session,
		opts:          opts,
		active:        "",
		lastDirection: 0,
		timer:         nil,
		timeout:       nil,
	}

	defer func() {
		_ = control.stop()

		if control.timer != nil {
			control.timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-interrupts:
			return nil
		case err := <-events:
			return err
		case err := <-mediaErr:
			return err
		case <-control.timeout:
			err := control.stop()
			if err != nil {
				return err
			}

			if opts.debug {
				_, _ = fmt.Fprintln(out, "PTZ idle stop acknowledged")
			}

			control.timeout = nil
		case key, ok := <-keys:
			if !ok || key == 'q' || key == 'Q' {
				return nil
			}

			err := control.key(ctx, key)
			if err != nil {
				return err
			}

			if opts.debug {
				if _, valid := keyAxis(key); valid {
					_, _ = fmt.Fprintf(out, "PTZ command acknowledged: %s\n", keyDirection(key))
				} else if key == ' ' {
					_, _ = fmt.Fprintln(out, "PTZ stop acknowledged")
				}
			}
		}
	}
}

func keyDirection(key rune) string {
	switch key {
	case 'A':
		return "up"
	case 'B':
		return "down"
	case 'C':
		return "right"
	case 'D':
		return "left"
	default:
		return "unknown"
	}
}

type ptzController struct {
	session       *ring.DeviceSession
	opts          viewOptions
	active        ring.PTZAxis
	lastDirection rune
	timer         *time.Timer
	timeout       <-chan time.Time
}

func (c *ptzController) stop() error {
	if c.active == "" {
		return nil
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), ptzStopTimeout)
	defer cancel()

	_, err := c.session.StopPTZ(stopCtx, ring.StopPTZRequest{Axis: c.active})
	if err == nil {
		c.active = ""
	}

	if err != nil {
		return wrapCommandError("stop PTZ movement", err)
	}

	return nil
}

func (c *ptzController) key(ctx context.Context, key rune) error {
	if key == ' ' {
		c.timeout = nil

		return c.stop()
	}

	axis, valid := keyAxis(key)
	if !valid {
		return nil
	}

	if c.active != "" && (axis != c.active || key != c.lastDirection) {
		err := c.stop()
		if err != nil {
			return err
		}
	}

	if !c.opts.continuous {
		return step(ctx, c.session, key)
	}

	if c.active == "" {
		err := continuous(ctx, c.session, key, c.opts.speed)
		if err != nil {
			return err
		}

		c.active, c.lastDirection = axis, key
	}

	if c.timer == nil {
		c.timer = time.NewTimer(ptzIdleTimeout)
	} else {
		c.timer.Reset(ptzIdleTimeout)
	}

	c.timeout = c.timer.C

	return nil
}

func keyAxis(key rune) (ring.PTZAxis, bool) {
	switch key {
	case 'C', 'D':
		return ring.PanAxis, true
	case 'A', 'B':
		return ring.TiltAxis, true
	default:
		return "", false
	}
}

func step(ctx context.Context, session *ring.DeviceSession, key rune) error {
	switch key {
	case 'C':
		_, err := session.PanStep(ctx, ring.PanStepRequest{Direction: ring.PanRight})
		if err != nil {
			return wrapCommandError("pan right", err)
		}
	case 'D':
		_, err := session.PanStep(ctx, ring.PanStepRequest{Direction: ring.PanLeft})
		if err != nil {
			return wrapCommandError("pan left", err)
		}
	case 'A':
		_, err := session.TiltStep(ctx, ring.TiltStepRequest{Direction: ring.TiltUp})
		if err != nil {
			return wrapCommandError("tilt up", err)
		}
	case 'B':
		_, err := session.TiltStep(ctx, ring.TiltStepRequest{Direction: ring.TiltDown})
		if err != nil {
			return wrapCommandError("tilt down", err)
		}
	default:
		return nil
	}

	return nil
}

func continuous(ctx context.Context, session *ring.DeviceSession, key rune, speed float64) error {
	switch key {
	case 'C':
		_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: speed})
		if err != nil {
			return wrapCommandError("pan right continuously", err)
		}
	case 'D':
		_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanLeft, Speed: speed})
		if err != nil {
			return wrapCommandError("pan left continuously", err)
		}
	case 'A':
		_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: speed})
		if err != nil {
			return wrapCommandError("tilt up continuously", err)
		}
	case 'B':
		_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltDown, Speed: speed})
		if err != nil {
			return wrapCommandError("tilt down continuously", err)
		}
	default:
		return nil
	}

	return nil
}
