// Package hatch implements the Muse "hatch" gateway protocol: a Noise XX
// encrypted WebSocket carrying protobuf-framed HTTP-like RPC.
//
// Wire shapes mirror the web bundle descriptors (see the reference
// implementation's wire.py):
//
//	NoiseTransportFrame { int64 chunk_id=1; uint32 chunk_index=2; uint32 total_chunks=3; bytes payload=4; }
//	ServiceRequest      { ServiceType service=1; bytes payload=2; }
//	ServiceResponse     { bytes payload=1; }
//	ServiceFrame        { int64 stream_id=1; oneof kind { ApplicationRequest request=2;
//	                       ApplicationResponse response=3; BodyChunk body_chunk=4; Reset reset=5; } }
//	ApplicationRequest  { string verb=1; string path=2; repeated Header headers=3; bytes body=4; bool end_body=5; }
//	ApplicationResponse { int32 status=1; repeated Header headers=2; bytes body=3; bool end_body=4; }
//	BodyChunk           { bytes data=1; bool end_body=2; }
//	Header              { string key=1; string value=2; }
//	Reset               { Code code=1; string reason=2; }
package hatch

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	// maxChunkPayload bounds a single NoiseTransportFrame payload.
	maxChunkPayload = 4 << 20
	maxChunks       = 256
	maxAssembly     = 16 << 20
	// Keep the total memory a peer can pin with incomplete assemblies bounded.
	maxPendingAssemblies = 32
	maxPendingBytes      = 64 << 20
)

// ---------------------------------------------------------------------------
// protobuf primitives
// ---------------------------------------------------------------------------

func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func fieldKey(field, wire int) []byte {
	return appendVarint(nil, uint64(field<<3|wire))
}

func fieldVarint(field int, v uint64) []byte {
	out := fieldKey(field, 0)
	return appendVarint(out, v)
}

func fieldBytes(field int, b []byte) []byte {
	out := fieldKey(field, 2)
	out = appendVarint(out, uint64(len(b)))
	return append(out, b...)
}

func fieldString(field int, s string) []byte { return fieldBytes(field, []byte(s)) }

func fieldBool(field int, v bool) []byte {
	if v {
		return fieldVarint(field, 1)
	}
	return fieldVarint(field, 0)
}

type protoField struct {
	num   int
	wire  int
	u64   uint64
	bytes []byte
}

func readVarint(data []byte, pos int) (uint64, int, error) {
	var v uint64
	var shift uint
	for {
		if pos >= len(data) {
			return 0, pos, errors.New("truncated varint")
		}
		b := data[pos]
		pos++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, pos, nil
		}
		shift += 7
		if shift > 70 {
			return 0, pos, errors.New("varint too long")
		}
	}
}

func parseFields(data []byte) ([]protoField, error) {
	out := make([]protoField, 0, 8)
	pos := 0
	for pos < len(data) {
		tag, next, err := readVarint(data, pos)
		if err != nil {
			return nil, err
		}
		pos = next
		num, wire := int(tag>>3), int(tag&7)
		switch wire {
		case 0:
			v, next, err := readVarint(data, pos)
			if err != nil {
				return nil, err
			}
			pos = next
			out = append(out, protoField{num: num, wire: wire, u64: v})
		case 2:
			length, next, err := readVarint(data, pos)
			if err != nil {
				return nil, err
			}
			pos = next
			end := pos + int(length)
			if end > len(data) || end < pos {
				return nil, errors.New("truncated length-delimited field")
			}
			out = append(out, protoField{num: num, wire: wire, bytes: data[pos:end]})
			pos = end
		case 1:
			if pos+8 > len(data) {
				return nil, errors.New("truncated fixed64")
			}
			out = append(out, protoField{num: num, wire: wire, bytes: data[pos : pos+8]})
			pos += 8
		case 5:
			if pos+4 > len(data) {
				return nil, errors.New("truncated fixed32")
			}
			out = append(out, protoField{num: num, wire: wire, bytes: data[pos : pos+4]})
			pos += 4
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
	}
	return out, nil
}

func signed64(v uint64) int64 { return int64(v) }

// ---------------------------------------------------------------------------
// application layer
// ---------------------------------------------------------------------------

// Header is one HTTP-like header on an application request/response.
type Header struct {
	Key   string
	Value string
}

func (h Header) encode() []byte {
	out := fieldString(1, h.Key)
	return append(out, fieldString(2, h.Value)...)
}

// ApplicationRequest is one RPC call (HTTP verb + path + headers + body).
type ApplicationRequest struct {
	Verb    string
	Path    string
	Headers []Header
	Body    []byte
	EndBody bool
}

func (r ApplicationRequest) encode() []byte {
	out := fieldString(1, r.Verb)
	out = append(out, fieldString(2, r.Path)...)
	for _, h := range r.Headers {
		out = append(out, fieldBytes(3, h.encode())...)
	}
	if len(r.Body) > 0 {
		out = append(out, fieldBytes(4, r.Body)...)
	}
	return append(out, fieldBool(5, r.EndBody)...)
}

// ApplicationResponse is the reply to one ApplicationRequest.
type ApplicationResponse struct {
	Status  int
	Headers []Header
	Body    []byte
	EndBody bool
}

func decodeApplicationResponse(data []byte) (ApplicationResponse, error) {
	var out ApplicationResponse
	fields, err := parseFields(data)
	if err != nil {
		return out, err
	}
	for _, f := range fields {
		switch {
		case f.num == 1 && f.wire == 0:
			out.Status = int(f.u64)
		case f.num == 2 && f.wire == 2:
			key, value := "", ""
			inner, err := parseFields(f.bytes)
			if err != nil {
				return out, err
			}
			for _, hf := range inner {
				if hf.num == 1 && hf.wire == 2 {
					key = string(hf.bytes)
				} else if hf.num == 2 && hf.wire == 2 {
					value = string(hf.bytes)
				}
			}
			out.Headers = append(out.Headers, Header{Key: key, Value: value})
		case f.num == 3 && f.wire == 2:
			out.Body = append(out.Body, f.bytes...)
		case f.num == 4 && f.wire == 0:
			out.EndBody = f.u64 != 0
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// service frames
// ---------------------------------------------------------------------------

const (
	serviceDaemon   = 0
	serviceSentinel = 1
	serviceVault    = 2
	serviceAuthd    = 3
)

func encodeServiceFrameRequest(streamID int64, req ApplicationRequest) []byte {
	out := fieldVarint(1, uint64(streamID))
	return append(out, fieldBytes(2, req.encode())...)
}

func encodeServiceFrameBody(streamID int64, data []byte, endBody bool) []byte {
	chunk := fieldBytes(1, data)
	chunk = append(chunk, fieldBool(2, endBody)...)
	out := fieldVarint(1, uint64(streamID))
	return append(out, fieldBytes(4, chunk)...)
}

func encodeServiceFrameReset(streamID int64, code int, reason string) []byte {
	reset := fieldVarint(1, uint64(code))
	reset = append(reset, fieldString(2, reason)...)
	out := fieldVarint(1, uint64(streamID))
	return append(out, fieldBytes(5, reset)...)
}

type serviceFrame struct {
	StreamID int64
	Kind     string // request | response | body_chunk | reset | unknown
	Request  []byte
	Response *ApplicationResponse
	BodyData []byte
	BodyEnd  bool
	Reset    int
	Reason   string
}

func decodeServiceFrame(data []byte) (serviceFrame, error) {
	var out serviceFrame
	fields, err := parseFields(data)
	if err != nil {
		return out, err
	}
	for _, f := range fields {
		switch {
		case f.num == 1 && f.wire == 0:
			out.StreamID = signed64(f.u64)
		case f.num == 2 && f.wire == 2:
			out.Kind = "request"
			out.Request = append([]byte(nil), f.bytes...)
		case f.num == 3 && f.wire == 2:
			resp, err := decodeApplicationResponse(f.bytes)
			if err != nil {
				return out, err
			}
			out.Kind = "response"
			out.Response = &resp
		case f.num == 4 && f.wire == 2:
			var chunkData []byte
			var end bool
			inner, err := parseFields(f.bytes)
			if err != nil {
				return out, err
			}
			for _, cf := range inner {
				if cf.num == 1 && cf.wire == 2 {
					chunkData = append([]byte(nil), cf.bytes...)
				} else if cf.num == 2 && cf.wire == 0 {
					end = cf.u64 != 0
				}
			}
			out.Kind = "body_chunk"
			out.BodyData = chunkData
			out.BodyEnd = end
		case f.num == 5 && f.wire == 2:
			code := 0
			reason := ""
			inner, err := parseFields(f.bytes)
			if err != nil {
				return out, err
			}
			for _, rf := range inner {
				if rf.num == 1 && rf.wire == 0 {
					code = int(rf.u64)
				} else if rf.num == 2 && rf.wire == 2 {
					reason = string(rf.bytes)
				}
			}
			out.Kind = "reset"
			out.Reset = code
			out.Reason = reason
		}
	}
	return out, nil
}

func encodeServiceRequest(service int, serviceFrameBytes []byte) []byte {
	out := fieldVarint(1, uint64(service))
	return append(out, fieldBytes(2, serviceFrameBytes)...)
}

func decodeServiceResponse(data []byte) ([]byte, error) {
	fields, err := parseFields(data)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if f.num == 1 && f.wire == 2 {
			return append([]byte(nil), f.bytes...), nil
		}
	}
	return nil, errors.New("service response missing payload")
}

// ---------------------------------------------------------------------------
// Noise transport chunking
// ---------------------------------------------------------------------------

type noiseChunk struct {
	ID      int64
	Index   int
	Total   int
	Payload []byte
}

func (c noiseChunk) encode() []byte {
	out := fieldVarint(1, uint64(c.ID))
	out = append(out, fieldVarint(2, uint64(c.Index))...)
	out = append(out, fieldVarint(3, uint64(c.Total))...)
	return append(out, fieldBytes(4, c.Payload)...)
}

func decodeNoiseChunk(data []byte) (noiseChunk, error) {
	out := noiseChunk{Total: 1}
	fields, err := parseFields(data)
	if err != nil {
		return out, err
	}
	for _, f := range fields {
		switch {
		case f.num == 1 && f.wire == 0:
			out.ID = signed64(f.u64)
		case f.num == 2 && f.wire == 0:
			out.Index = int(f.u64)
		case f.num == 3 && f.wire == 0:
			out.Total = int(f.u64)
		case f.num == 4 && f.wire == 2:
			out.Payload = append([]byte(nil), f.bytes...)
		}
	}
	return out, nil
}

func splitNoisePayload(data []byte) ([][]byte, error) {
	total := (len(data) + maxChunkPayload - 1) / maxChunkPayload
	if total == 0 {
		total = 1
	}
	if total > maxChunks {
		return nil, fmt.Errorf("payload too large for Noise framing: %d chunks", total)
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	id := int64(binary.LittleEndian.Uint64(idBytes))
	if len(data) == 0 {
		return [][]byte{noiseChunk{ID: id, Index: 0, Total: 1}.encode()}, nil
	}
	out := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		end := (i + 1) * maxChunkPayload
		if end > len(data) {
			end = len(data)
		}
		out = append(out, noiseChunk{ID: id, Index: i, Total: total, Payload: data[i*maxChunkPayload : end]}.encode())
	}
	return out, nil
}

type reassembler struct {
	pending    map[int64]*assembly
	totalBytes int
}

type assembly struct {
	total   int
	parts   map[int][]byte
	size    int
	updated time.Time
}

func newReassembler() *reassembler {
	return &reassembler{pending: map[int64]*assembly{}}
}

func (r *reassembler) drop(id int64) {
	if old := r.pending[id]; old != nil {
		r.totalBytes -= old.size
		delete(r.pending, id)
	}
}

func (r *reassembler) evictOldest() {
	var oldestID int64
	var oldest time.Time
	found := false
	for id, asm := range r.pending {
		if !found || asm.updated.Before(oldest) {
			oldestID, oldest, found = id, asm.updated, true
		}
	}
	if found {
		r.drop(oldestID)
	}
}

// feed returns (payload, complete, error). Incomplete multi-chunk payloads
// return complete=false. Completed assemblies are removed immediately; the
// old implementation leaked one order entry per inbound frame and its
// eviction therefore stopped working on long-lived connections.
func (r *reassembler) feed(raw []byte) ([]byte, bool, error) {
	chunk, err := decodeNoiseChunk(raw)
	if err != nil {
		return nil, false, err
	}
	if chunk.Total < 1 || chunk.Total > maxChunks {
		return nil, false, errors.New("invalid total_chunks")
	}
	if chunk.Index < 0 || chunk.Index >= chunk.Total {
		return nil, false, errors.New("invalid chunk_index")
	}
	if len(chunk.Payload) > maxChunkPayload {
		r.drop(chunk.ID)
		return nil, false, errors.New("noise frame payload too large")
	}
	asm := r.pending[chunk.ID]
	if asm == nil {
		for len(r.pending) >= maxPendingAssemblies || (r.totalBytes+len(chunk.Payload) > maxPendingBytes && len(r.pending) > 0) {
			r.evictOldest()
		}
		if r.totalBytes+len(chunk.Payload) > maxPendingBytes {
			return nil, false, errors.New("pending noise assemblies exceeded memory budget")
		}
		asm = &assembly{total: chunk.Total, parts: map[int][]byte{}, updated: time.Now()}
		r.pending[chunk.ID] = asm
	}
	if asm.total != chunk.Total {
		r.drop(chunk.ID)
		return nil, false, errors.New("inconsistent total_chunks")
	}
	if _, exists := asm.parts[chunk.Index]; exists {
		r.drop(chunk.ID)
		return nil, false, errors.New("duplicate noise chunk")
	}
	asm.parts[chunk.Index] = chunk.Payload
	asm.size += len(chunk.Payload)
	asm.updated = time.Now()
	r.totalBytes += len(chunk.Payload)
	if asm.size > maxAssembly {
		r.drop(chunk.ID)
		return nil, false, errors.New("noise assembly exceeded 16 MiB")
	}
	if len(asm.parts) != asm.total {
		return nil, false, nil
	}
	r.drop(chunk.ID)
	out := make([]byte, 0, asm.size)
	for i := 0; i < asm.total; i++ {
		out = append(out, asm.parts[i]...)
	}
	return out, true, nil
}
