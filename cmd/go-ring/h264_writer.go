package main

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

const (
	h264NALTypeMask   = 0x1f
	h264SPS           = 7
	h264PPS           = 8
	h264IDR           = 5
	h264STAPA         = 24
	h264FUA           = 28
	minFUAPayloadSize = 3
	maxH264FrameBytes = 8 << 20
)

var h264StartCode = []byte{0, 0, 0, 1}

// h264FrameWriter forwards only complete access units. A missing RTP packet
// invalidates the current unit and predictive frames until the next IDR.
type h264FrameWriter struct {
	output       io.Writer
	depacketizer codecs.H264Packet
	frame        []byte
	sps          []byte
	pps          []byte
	timestamp    uint32
	sequence     uint16
	haveTime     bool
	haveSequence bool
	damaged      bool
	ready        bool
}

func newH264FrameWriter(output io.Writer) *h264FrameWriter {
	return &h264FrameWriter{output: output}
}

func (w *h264FrameWriter) WriteRTP(packet *rtp.Packet) error {
	if len(packet.Payload) == 0 {
		return nil
	}
	if w.haveTime && packet.Timestamp != w.timestamp {
		w.ready = false
		w.resetFrame()
	}
	if w.haveSequence && packet.SequenceNumber != w.sequence+1 {
		w.ready = false
		if w.haveTime {
			w.damaged = true
		}
	}
	w.sequence, w.haveSequence = packet.SequenceNumber, true
	if !w.haveTime {
		w.timestamp, w.haveTime = packet.Timestamp, true
		if !w.depacketizer.IsPartitionHead(packet.Payload) {
			w.damaged = true
		}
	}
	if !w.damaged {
		if !validH264Payload(packet.Payload) {
			w.damaged = true
		} else if data, err := w.depacketizer.Unmarshal(packet.Payload); err != nil || len(w.frame)+len(data) > maxH264FrameBytes {
			w.damaged = true
		} else {
			w.frame = append(w.frame, data...)
		}
	}
	if !packet.Marker {
		return nil
	}
	var err error
	if !w.damaged {
		err = w.writeFrame()
	}
	w.resetFrame()
	return err
}

func validH264Payload(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	switch kind := payload[0] & h264NALTypeMask; {
	case kind > 0 && kind < h264STAPA:
		return true
	case kind == h264FUA:
		return len(payload) >= minFUAPayloadSize
	case kind == h264STAPA:
		for offset := 1; offset < len(payload); {
			if offset+2 > len(payload) {
				return false
			}
			size := int(binary.BigEndian.Uint16(payload[offset:]))
			offset += 2
			if size == 0 || offset+size > len(payload) {
				return false
			}
			offset += size
		}
		return true
	default:
		return false
	}
}

func (w *h264FrameWriter) writeFrame() error {
	var hasIDR bool
	for _, nalu := range bytes.Split(w.frame, h264StartCode) {
		if len(nalu) == 0 {
			continue
		}
		switch nalu[0] & h264NALTypeMask {
		case h264SPS:
			w.sps = append(w.sps[:0], nalu...)
		case h264PPS:
			w.pps = append(w.pps[:0], nalu...)
		case h264IDR:
			hasIDR = true
		}
	}
	if hasIDR {
		if len(w.sps) == 0 || len(w.pps) == 0 {
			w.ready = false
			return nil
		}
		if _, err := w.output.Write(h264StartCode); err != nil {
			return err
		}
		if _, err := w.output.Write(w.sps); err != nil {
			return err
		}
		if _, err := w.output.Write(h264StartCode); err != nil {
			return err
		}
		if _, err := w.output.Write(w.pps); err != nil {
			return err
		}
		w.ready = true
	}
	if !w.ready {
		return nil
	}
	_, err := w.output.Write(w.frame)
	return err
}

func (w *h264FrameWriter) resetFrame() {
	w.frame = w.frame[:0]
	w.depacketizer = codecs.H264Packet{}
	w.haveTime = false
	w.damaged = false
}

func (w *h264FrameWriter) Close() error {
	if closer, ok := w.output.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
