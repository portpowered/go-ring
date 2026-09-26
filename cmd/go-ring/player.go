package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pion/webrtc/v3"
)

func playTrack(ctx context.Context, track *webrtc.TrackRemote) error {
	codec := track.Codec()
	name := strings.TrimPrefix(strings.ToUpper(codec.MimeType), "VIDEO/")
	if name != "H264" && name != "VP8" {
		return fmt.Errorf("ffplay preview does not support negotiated codec %s", codec.MimeType)
	}
	if _, err := exec.LookPath(ffplayCommand); err != nil {
		return errors.New("ffplay is required for preview; install FFmpeg or use --player none")
	}
	address, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	reserve, err := net.ListenUDP("udp4", address)
	if err != nil {
		return err
	}
	port := reserve.LocalAddr().(*net.UDPAddr).Port
	_ = reserve.Close()
	file, err := os.CreateTemp("", "go-ring-preview-*.sdp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	fmtp := ""
	if codec.SDPFmtpLine != "" {
		fmtp = fmt.Sprintf("a=fmtp:%d %s\r\n", track.PayloadType(), codec.SDPFmtpLine)
	}
	sdp := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=go-ring preview\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=video %d RTP/AVP %d\r\na=rtpmap:%d %s/%d\r\n%s", port, track.PayloadType(), track.PayloadType(), name, codec.ClockRate, fmtp)
	if _, err := file.WriteString(sdp); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, ffplayCommand, "-loglevel", "error", "-protocol_whitelist", "file,udp,rtp", "-fflags", "nobuffer", "-flags", "low_delay", "-i", file.Name())
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	target, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(loopbackFirstOctet, 0, 0, 1), Port: port})
	if err != nil {
		return err
	}
	defer target.Close()
	// Let ffplay bind its local RTP port before the first packet arrives.
	select {
	case <-time.After(playerStartupDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return err
		}
		data, err := packet.Marshal()
		if err != nil {
			return err
		}
		if _, err := target.Write(data); err != nil {
			return err
		}
	}
}
