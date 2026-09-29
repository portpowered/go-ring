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

const h264StartCode = "\x00\x00\x00\x01"

// h264FrameWriter forwards only complete access units. A missing RTP packet
// invalidates the current unit and predictive frames until the next IDR.
type h264FrameWriter struct {
	output       io.Writer
	depacketizer codecs.H264Packet
	frame        []byte
	sps          map[uint64][]byte
	pps          map[uint64]h264PPSInfo
	timestamp    uint32
	sequence     uint16
	haveTime     bool
	haveSequence bool
	damaged      bool
	ready        bool
}

type h264PPSInfo struct {
	spsID uint64
	data  []byte
}

func newH264FrameWriter(output io.Writer) *h264FrameWriter {
	return &h264FrameWriter{
		output:       output,
		depacketizer: codecs.H264Packet{IsAVC: false},
		frame:        nil,
		sps:          make(map[uint64][]byte),
		pps:          make(map[uint64]h264PPSInfo),
		timestamp:    0,
		sequence:     0,
		haveTime:     false,
		haveSequence: false,
		damaged:      false,
		ready:        false,
	}
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
		w.appendPacketPayload(packet.Payload)
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

func (w *h264FrameWriter) Close() error {
	if closer, ok := w.output.(io.Closer); ok {
		err := closer.Close()
		if err != nil {
			return wrapCommandError("close H264 output", err)
		}
	}

	return nil
}

func (w *h264FrameWriter) appendPacketPayload(payload []byte) {
	if !validH264Payload(payload) {
		w.damaged = true

		return
	}

	data, err := w.depacketizer.Unmarshal(payload)
	if err != nil || len(w.frame)+len(data) > maxH264FrameBytes {
		w.damaged = true

		return
	}

	w.frame = append(w.frame, data...)
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
	nalus := bytes.Split(w.frame, []byte(h264StartCode))
	references, hasIDR, valid := w.collectParameterReferences(nalus)

	if !valid || !w.hasReferencedParameterSets(references) {
		w.ready = false

		return nil
	}

	if hasIDR {
		err := w.writeParameterSets(references)
		if err != nil {
			return err
		}

		w.ready = true
	}

	if !w.ready {
		return nil
	}

	_, err := w.output.Write(w.frame)
	if err != nil {
		return wrapCommandError("write H264 frame", err)
	}

	return nil
}

func (w *h264FrameWriter) collectParameterReferences(nalus [][]byte) ([]uint64, bool, bool) {
	var (
		hasIDR     bool
		references []uint64
	)

	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}

		switch nalu[0] & h264NALTypeMask {
		case h264SPS:
			id, ok := h264SPSID(nalu)
			if !ok {
				return nil, false, false
			}

			w.sps[id] = append(w.sps[id][:0], nalu...)
		case h264PPS:
			id, spsID, ok := h264PPSIDs(nalu)
			if !ok {
				return nil, false, false
			}

			w.pps[id] = h264PPSInfo{spsID: spsID, data: append(w.pps[id].data[:0], nalu...)}
		case h264IDR:
			hasIDR = true

			fallthrough
		case 1:
			id, ok := h264SlicePPSID(nalu)
			if !ok {
				return nil, false, false
			}

			references = append(references, id)
		}
	}

	return references, hasIDR, true
}

func (w *h264FrameWriter) hasReferencedParameterSets(references []uint64) bool {
	for _, id := range references {
		pps, ok := w.pps[id]
		if !ok || len(w.sps[pps.spsID]) == 0 {
			return false
		}
	}

	return true
}

func (w *h264FrameWriter) writeParameterSets(references []uint64) error {
	for _, id := range references {
		pps := w.pps[id]
		for _, nalu := range [][]byte{w.sps[pps.spsID], pps.data} {
			_, err := w.output.Write([]byte(h264StartCode))
			if err != nil {
				return wrapCommandError("write H264 start code", err)
			}

			_, err = w.output.Write(nalu)
			if err != nil {
				return wrapCommandError("write H264 parameter set", err)
			}
		}
	}

	return nil
}

func (w *h264FrameWriter) resetFrame() {
	w.frame = w.frame[:0]
	w.depacketizer = codecs.H264Packet{IsAVC: false}
	w.haveTime = false
	w.damaged = false
}
