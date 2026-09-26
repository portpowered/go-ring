package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	continuous bool
	speed      float64
	debug      bool
}

func viewCommand(parent context.Context, store tokenStore, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("view requires a device ID")
	}
	flags := flag.NewFlagSet("view", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	player := flags.String("player", ffplayCommand, "ffplay or none")
	iceFile := flags.String("ice-servers", "", "JSON array of ICE servers")
	continuous := flags.Bool("continuous", false, "continuous PTZ with inactivity stop")
	speed := flags.Float64("speed", defaultPTZSpeed, "continuous PTZ speed from 0 to 1")
	debug := flags.Bool("debug", false, "show connection and control diagnostics")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*player != "none" && *player != ffplayCommand) || *speed <= 0 || *speed > 1 {
		return errors.New("invalid view options")
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	return withClient(parent, store, func(client *ring.Client, auth ring.AuthContext) error {
		return view(parent, client, auth, args[0], viewOptions{player: *player, iceFile: *iceFile, continuous: *continuous, speed: *speed, debug: *debug}, in, out, interrupts)
	})
}

func view(parent context.Context, client *ring.Client, auth ring.AuthContext, deviceID string, opts viewOptions, in io.Reader, out io.Writer, interrupts <-chan os.Signal) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	config, err := videoConfiguration(opts.iceFile)
	if err != nil {
		return err
	}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer pc.Close()
	if opts.debug {
		pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) { _, _ = fmt.Fprintf(out, "ICE connection: %s\n", state) })
		pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) { _, _ = fmt.Fprintf(out, "Peer connection: %s\n", state) })
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}
	mediaErr := make(chan error, 1)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		_, _ = fmt.Fprintf(out, "Video track: %s\n", track.Codec().MimeType)
		if opts.player == ffplayCommand {
			if err := playTrack(ctx, track); err != nil && ctx.Err() == nil {
				select {
				case mediaErr <- err:
				default:
				}
			}
			return
		}
		var packets uint64
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
			packets++
			if packets == 1 {
				_, _ = fmt.Fprintln(out, "First video packet received")
			}
		}
	})
	conn, session, err := startVideoSession(ctx, client, auth, deviceID, pc)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer session.Close()
	if opts.debug {
		_, _ = fmt.Fprintln(out, "Remote SDP answer applied")
	}
	events := make(chan error, 1)
	go receiveICE(ctx, session, pc, events)
	keys := make(chan rune)
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		old, err := term.MakeRaw(int(file.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(file.Fd()), old)
	}
	go readKeys(in, keys)
	_, _ = fmt.Fprintln(out, "Session active. Arrow keys move camera; Space stops; q quits.")
	return controlLoop(ctx, session, opts, keys, events, mediaErr, interrupts, out)
}

func videoConfiguration(iceFile string) (webrtc.Configuration, error) {
	config := webrtc.Configuration{}
	if iceFile == "" {
		return config, nil
	}
	data, err := os.ReadFile(iceFile)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(data, &config.ICEServers); err != nil {
		return config, errors.New("invalid ICE server JSON")
	}
	return config, nil
}

func startVideoSession(ctx context.Context, client *ring.Client, auth ring.AuthContext, deviceID string, pc *webrtc.PeerConnection) (*ring.SignalingConnection, *ring.DeviceSession, error) {
	offer, err := makeVideoOffer(ctx, pc)
	if err != nil {
		return nil, nil, err
	}
	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
	if err != nil {
		return nil, nil, err
	}
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: deviceID, Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, VideoEnabled: true, ICEMode: ring.ICENonTrickle})
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: session.Answer().SDP}); err != nil {
		_ = session.Close()
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, session, nil
}

func makeVideoOffer(ctx context.Context, pc *webrtc.PeerConnection) (string, error) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	complete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	timer := time.NewTimer(offerTimeout)
	defer timer.Stop()
	select {
	case <-complete:
		return pc.LocalDescription().SDP, nil
	case <-timer.C:
		return "", errors.New("ICE gathering timed out")
	case <-ctx.Done():
		return "", ctx.Err()
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
			done <- errors.New("invalid remote ICE candidate")
			return
		}
		if err := pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: body.Candidate, SDPMLineIndex: &body.Index}); err != nil {
			done <- err
			return
		}
	}
}

func readKeys(in io.Reader, keys chan<- rune) {
	defer close(keys)
	reader := bufio.NewReader(in)
	for {
		char, err := reader.ReadByte()
		if err != nil {
			return
		}
		if char == escapeKey {
			lead, err := reader.ReadByte()
			if err != nil {
				return
			}
			if lead != '[' {
				continue
			}
			char, err = reader.ReadByte()
			if err != nil {
				return
			}
		} else if char == 0 || char == 0xe0 {
			windowsKey, err := reader.ReadByte()
			if err != nil {
				return
			}
			switch windowsKey {
			case 'H':
				char = 'A'
			case 'P':
				char = 'B'
			case 'M':
				char = 'C'
			case 'K':
				char = 'D'
			default:
				continue
			}
		}
		keys <- rune(char)
	}
}

func controlLoop(ctx context.Context, session *ring.DeviceSession, opts viewOptions, keys <-chan rune, events, mediaErr <-chan error, interrupts <-chan os.Signal, out io.Writer) error {
	control := &ptzController{session: session, opts: opts}
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
			if err := control.stop(); err != nil {
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
			if err := control.key(ctx, key); err != nil {
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
	return err
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
		if err := c.stop(); err != nil {
			return err
		}
	}
	if !c.opts.continuous {
		return step(ctx, c.session, key)
	}
	if c.active == "" {
		if err := continuous(ctx, c.session, key, c.opts.speed); err != nil {
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
		return err
	case 'D':
		_, err := session.PanStep(ctx, ring.PanStepRequest{Direction: ring.PanLeft})
		return err
	case 'A':
		_, err := session.TiltStep(ctx, ring.TiltStepRequest{Direction: ring.TiltUp})
		return err
	case 'B':
		_, err := session.TiltStep(ctx, ring.TiltStepRequest{Direction: ring.TiltDown})
		return err
	default:
		return nil
	}
}

func continuous(ctx context.Context, session *ring.DeviceSession, key rune, speed float64) error {
	switch key {
	case 'C':
		_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: speed})
		return err
	case 'D':
		_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanLeft, Speed: speed})
		return err
	case 'A':
		_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltUp, Speed: speed})
		return err
	case 'B':
		_, err := session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: ring.TiltDown, Speed: speed})
		return err
	default:
		return nil
	}
}
