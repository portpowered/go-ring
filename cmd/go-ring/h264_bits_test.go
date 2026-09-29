package main

import "testing"

func TestH264ParameterSetIDs(t *testing.T) {
	t.Parallel()

	if id, ok := h264SPSID([]byte{0x67, 0x64, 0, 0x28, 0x10}); !ok || id != 7 {
		t.Fatalf("SPS ID = %d, valid = %t", id, ok)
	}

	if ppsID, spsID, ok := h264PPSIDs([]byte{0x68, 0x10, 0x22, 0xe3, 0x88}); !ok || ppsID != 7 || spsID != 7 {
		t.Fatalf("PPS/SPS IDs = %d/%d, valid = %t", ppsID, spsID, ok)
	}

	if id, ok := h264SlicePPSID([]byte{0x65, 0xd8}); !ok || id != 2 {
		t.Fatalf("slice PPS ID = %d, valid = %t", id, ok)
	}
}
