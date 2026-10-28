package browser

import "encoding/binary"

type mp4Box struct {
	typ     string
	payload []byte
}

func readMP4Boxes(data []byte) []mp4Box {
	boxes := make([]mp4Box, 0, 8)
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data[0:4]))
		typ := string(data[4:8])
		header := 8
		switch size {
		case 1:
			if len(data) < 16 {
				return boxes
			}
			size = binary.BigEndian.Uint64(data[8:16])
			header = 16
		case 0:
			size = uint64(len(data))
		}
		if size < uint64(header) || size > uint64(len(data)) {
			break
		}
		boxes = append(boxes, mp4Box{typ: typ, payload: data[header:int(size)]})
		data = data[int(size):]
	}
	return boxes
}

func findMP4Box(boxes []mp4Box, typ string) (mp4Box, bool) {
	for _, box := range boxes {
		if box.typ == typ {
			return box, true
		}
	}
	return mp4Box{}, false
}

func parseMVHD(payload []byte) (int, bool) {
	if len(payload) < 4 {
		return 0, false
	}
	switch payload[0] {
	case 0:
		if len(payload) < 20 {
			return 0, false
		}
		timescale := binary.BigEndian.Uint32(payload[12:16])
		duration := binary.BigEndian.Uint32(payload[16:20])
		if timescale == 0 {
			return 0, false
		}
		return int(uint64(duration) * 1000 / uint64(timescale)), true
	case 1:
		if len(payload) < 32 {
			return 0, false
		}
		timescale := binary.BigEndian.Uint32(payload[20:24])
		duration := binary.BigEndian.Uint64(payload[24:32])
		if timescale == 0 {
			return 0, false
		}
		return int(duration * 1000 / uint64(timescale)), true
	}
	return 0, false
}

func parseTKHD(payload []byte) (int, int, bool) {
	if len(payload) < 4 {
		return 0, 0, false
	}
	offset := 0
	switch payload[0] {
	case 0:
		offset = 76
	case 1:
		offset = 88
	default:
		return 0, 0, false
	}
	if len(payload) < offset+8 {
		return 0, 0, false
	}
	width := binary.BigEndian.Uint32(payload[offset : offset+4])
	height := binary.BigEndian.Uint32(payload[offset+4 : offset+8])
	if width == 0 || height == 0 {
		return 0, 0, false
	}
	return int(width >> 16), int(height >> 16), true
}

// probeMP4Metadata reads the movie header and the first video track header.
// It is intentionally best-effort: a malformed or fragmented file returns
// ok=false and the task simply omits duration/resolution.
func probeMP4Metadata(data []byte) (durationMS, width, height int, ok bool) {
	top := readMP4Boxes(data)
	moov, found := findMP4Box(top, "moov")
	if !found {
		return 0, 0, 0, false
	}
	children := readMP4Boxes(moov.payload)
	if mvhd, found := findMP4Box(children, "mvhd"); found {
		if value, valid := parseMVHD(mvhd.payload); valid {
			durationMS = value
			ok = true
		}
	}
	for _, child := range children {
		if child.typ != "trak" {
			continue
		}
		trakChildren := readMP4Boxes(child.payload)
		tkhd, found := findMP4Box(trakChildren, "tkhd")
		if !found {
			continue
		}
		if w, h, valid := parseTKHD(tkhd.payload); valid {
			width, height = w, h
			ok = true
			break
		}
	}
	return durationMS, width, height, ok
}
