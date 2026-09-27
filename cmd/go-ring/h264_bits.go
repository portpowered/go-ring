package main

const (
	h264BitsPerByte       = 8
	h264HighBitOffset     = h264BitsPerByte - 1
	h264MaxExpGolombZeros = 31
	h264MinSPSBytes       = 4
	h264SPSHeaderBits     = 24
	h264MaxSPSID          = 31
	h264MaxPPSID          = 255
)

// H264 parameter-set IDs are unsigned Exp-Golomb values inside the RBSP.
// Reading only these IDs lets us reject slices whose required PPS was not
// received without trying to implement a full H264 decoder.
type h264Bits struct {
	data []byte
	bit  int
}

func h264RBSP(nalu []byte) h264Bits {
	data := make([]byte, 0, len(nalu))
	for _, value := range nalu[1:] {
		if len(data) >= 2 && data[len(data)-2] == 0 && data[len(data)-1] == 0 && value == 3 {
			continue
		}
		data = append(data, value)
	}
	return h264Bits{data: data}
}

func (b *h264Bits) bitValue() (uint64, bool) {
	if b.bit >= len(b.data)*h264BitsPerByte {
		return 0, false
	}
	value := uint64((b.data[b.bit/h264BitsPerByte] >> (h264HighBitOffset - b.bit%h264BitsPerByte)) & 1)
	b.bit++
	return value, true
}

func (b *h264Bits) skip(count int) bool {
	if b.bit+count > len(b.data)*h264BitsPerByte {
		return false
	}
	b.bit += count
	return true
}

func (b *h264Bits) ue() (uint64, bool) {
	zeros := 0
	for {
		bit, ok := b.bitValue()
		if !ok {
			return 0, false
		}
		if bit != 0 {
			break
		}
		zeros++
		if zeros > h264MaxExpGolombZeros {
			return 0, false
		}
	}
	value := uint64(1)
	for range zeros {
		bit, ok := b.bitValue()
		if !ok {
			return 0, false
		}
		value = value<<1 | bit
	}
	return value - 1, true
}

func h264SPSID(nalu []byte) (uint64, bool) {
	if len(nalu) < h264MinSPSBytes {
		return 0, false
	}
	bits := h264RBSP(nalu)
	if !bits.skip(h264SPSHeaderBits) {
		return 0, false
	}
	id, ok := bits.ue()
	return id, ok && id <= h264MaxSPSID
}

func h264PPSIDs(nalu []byte) (uint64, uint64, bool) {
	if len(nalu) < 2 {
		return 0, 0, false
	}
	bits := h264RBSP(nalu)
	ppsID, ok := bits.ue()
	if !ok {
		return 0, 0, false
	}
	spsID, ok := bits.ue()
	return ppsID, spsID, ok && ppsID <= h264MaxPPSID && spsID <= h264MaxSPSID
}

func h264SlicePPSID(nalu []byte) (uint64, bool) {
	if len(nalu) < 2 {
		return 0, false
	}
	bits := h264RBSP(nalu)
	if _, ok := bits.ue(); !ok {
		return 0, false
	}
	if _, ok := bits.ue(); !ok {
		return 0, false
	}
	id, ok := bits.ue()
	return id, ok && id <= h264MaxPPSID
}
