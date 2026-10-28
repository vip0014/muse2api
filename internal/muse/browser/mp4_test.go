package browser

import (
	"encoding/binary"
	"testing"
)

func mp4TestBox(typ string, payload ...[]byte) []byte {
	size := 8
	for _, part := range payload {
		size += len(part)
	}
	out := make([]byte, size)
	binary.BigEndian.PutUint32(out[0:4], uint32(size))
	copy(out[4:8], typ)
	offset := 8
	for _, part := range payload {
		copy(out[offset:], part)
		offset += len(part)
	}
	return out
}

func mvhdV0(timescale, duration uint32) []byte {
	payload := make([]byte, 20)
	binary.BigEndian.PutUint32(payload[12:16], timescale)
	binary.BigEndian.PutUint32(payload[16:20], duration)
	return mp4TestBox("mvhd", payload)
}

func tkhdV0(width, height uint32) []byte {
	payload := make([]byte, 84)
	binary.BigEndian.PutUint32(payload[76:80], width<<16)
	binary.BigEndian.PutUint32(payload[80:84], height<<16)
	return mp4TestBox("tkhd", payload)
}

func TestProbeMP4Metadata(t *testing.T) {
	audio := mp4TestBox("trak", tkhdV0(0, 0))
	video := mp4TestBox("trak", tkhdV0(1280, 720))
	moov := mp4TestBox("moov", mvhdV0(1000, 6000), audio, video)
	data := append(mp4TestBox("ftyp", []byte("isom")), moov...)

	duration, width, height, ok := probeMP4Metadata(data)
	if !ok || duration != 6000 || width != 1280 || height != 720 {
		t.Fatalf("unexpected metadata: duration=%d width=%d height=%d ok=%v", duration, width, height, ok)
	}
}

func TestProbeMP4MetadataRejectsMalformedInput(t *testing.T) {
	if _, _, _, ok := probeMP4Metadata([]byte("not an mp4")); ok {
		t.Fatal("malformed input accepted")
	}
}
