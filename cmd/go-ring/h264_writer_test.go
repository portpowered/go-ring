package main

import (
	"bytes"
	"testing"

	"github.com/pion/rtp"
)

func TestH264WriterWaitsForParametersAndDropsIncompleteFrames(t *testing.T) {
	var output bytes.Buffer
	writer := newH264FrameWriter(&output)
	packet := func(sequence uint16, timestamp uint32, marker bool, payload ...byte) *rtp.Packet {
		return &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: sequence, Timestamp: timestamp, Marker: marker}, Payload: payload}
	}
	initial := []*rtp.Packet{
		packet(1, 100, true, 0x41, 0xe0, 0x01), // P slice before SPS/PPS must not reach the decoder.
		packet(2, 200, false, 0x67, 0x42, 0x00, 0x1f, 0x80),
		packet(3, 200, false, 0x68, 0xc0),
		packet(4, 200, true, 0x65, 0xb8, 0x84),
	}
	for _, p := range initial {
		if err := writer.WriteRTP(p); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x65, 0xb8, 0x84}) {
		t.Fatal("complete IDR was not written")
	}
	if bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x41, 0xe0, 0x01}) {
		t.Fatal("pre-parameter P slice reached decoder")
	}
	goodLength := output.Len()
	for _, p := range []*rtp.Packet{
		packet(5, 300, false, 0x7c, 0x81, 0xaa), // FU-A start.
		packet(7, 300, true, 0x7c, 0x41, 0xbb),  // Missing sequence 6 corrupts the unit.
		packet(8, 400, true, 0x41, 0xe0, 0x02),  // Predictive frame after loss is unsafe.
	} {
		if err := writer.WriteRTP(p); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != goodLength {
		t.Fatal("damaged or predictive frame reached decoder")
	}
	if err := writer.WriteRTP(packet(9, 500, true, 0x65, 0xb8, 0x99)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes()[goodLength:], []byte{0, 0, 0, 1, 0x65, 0xb8, 0x99}) {
		t.Fatal("writer did not resume at next IDR")
	}
}

func TestH264WriterAcceptsSTAPAAndRejectsTruncatedPackets(t *testing.T) {
	var output bytes.Buffer
	writer := newH264FrameWriter(&output)
	packets := []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1, Timestamp: 100, Marker: true}, Payload: []byte{0x78, 0x00}}, // Truncated STAP-A.
		{Header: rtp.Header{SequenceNumber: 2, Timestamp: 200}, Payload: []byte{0x78, 0, 5, 0x67, 0x42, 0, 0x1f, 0x80, 0, 2, 0x68, 0xc0}},
		{Header: rtp.Header{SequenceNumber: 3, Timestamp: 200, Marker: true}, Payload: []byte{0x65, 0xb8}},
	}
	for _, p := range packets {
		if err := writer.WriteRTP(p); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x67, 0x42}) || !bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x68, 0xc0}) {
		t.Fatal("STAP-A SPS/PPS were not preserved")
	}
}

func TestH264WriterWaitsForPPSBeforeIDR(t *testing.T) {
	var output bytes.Buffer
	writer := newH264FrameWriter(&output)
	packets := []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1, Timestamp: 100, Marker: true}, Payload: []byte{0x67, 0x42, 0, 0x1f, 0x80}},
		{Header: rtp.Header{SequenceNumber: 2, Timestamp: 200, Marker: true}, Payload: []byte{0x65, 0xb8}},
	}
	for _, packet := range packets {
		if err := writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != 0 {
		t.Fatal("IDR without PPS reached decoder")
	}
	for _, packet := range []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 3, Timestamp: 300, Marker: true}, Payload: []byte{0x68, 0xc0}},
		{Header: rtp.Header{SequenceNumber: 4, Timestamp: 400, Marker: true}, Payload: []byte{0x65, 0xb8, 0x99}},
	} {
		if err := writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x65, 0xb8, 0x99}) {
		t.Fatal("IDR was not written after PPS became available")
	}
}

func TestH264WriterRejectsSliceReferencingUnknownPPS(t *testing.T) {
	var output bytes.Buffer
	writer := newH264FrameWriter(&output)
	packets := []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 1, Timestamp: 100, Marker: true}, Payload: []byte{0x67, 0x42, 0, 0x1f, 0x80}}, // SPS 0.
		{Header: rtp.Header{SequenceNumber: 2, Timestamp: 200, Marker: true}, Payload: []byte{0x68, 0xc0}},                // PPS 0.
		{Header: rtp.Header{SequenceNumber: 3, Timestamp: 300, Marker: true}, Payload: []byte{0x65, 0xd8}},                // IDR uses PPS 2.
	}
	for _, packet := range packets {
		if err := writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != 0 {
		t.Fatal("IDR referencing absent PPS 2 reached decoder")
	}
	for _, packet := range []*rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 4, Timestamp: 400, Marker: true}, Payload: []byte{0x68, 0x70}}, // PPS 2 uses SPS 0.
		{Header: rtp.Header{SequenceNumber: 5, Timestamp: 500, Marker: true}, Payload: []byte{0x65, 0xd8}},
	} {
		if err := writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x68, 0x70}) || !bytes.Contains(output.Bytes(), []byte{0, 0, 0, 1, 0x65, 0xd8}) {
		t.Fatal("IDR referencing known PPS 2 was not written with its parameter set")
	}
}
