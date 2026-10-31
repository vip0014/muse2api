package hatch

import (
	"bytes"
	"testing"
)

func TestNoiseXXHandshakeAndSplit(t *testing.T) {
	ini := NewNoiseXXInitiator()
	resp := newNoiseXXResponder()

	nonce := bytes.Repeat([]byte{0x42}, 32)
	m1, err := ini.WriteMessage1(append([]byte{0x0a, 0x20}, nonce...))
	if err != nil {
		t.Fatalf("message1: %v", err)
	}
	p1, err := resp.ReadMessage1(m1)
	if err != nil {
		t.Fatalf("responder read message1: %v", err)
	}
	if !bytes.Equal(p1, append([]byte{0x0a, 0x20}, nonce...)) {
		t.Fatalf("message1 payload mismatch")
	}

	m2, err := resp.WriteMessage2([]byte("attestation-blob"))
	if err != nil {
		t.Fatalf("message2: %v", err)
	}
	p2, err := ini.ReadMessage2(m2)
	if err != nil {
		t.Fatalf("initiator read message2: %v", err)
	}
	if string(p2) != "attestation-blob" {
		t.Fatalf("message2 payload mismatch: %q", p2)
	}

	m3, err := ini.WriteMessage3(nil)
	if err != nil {
		t.Fatalf("message3: %v", err)
	}
	if _, err := resp.ReadMessage3(m3); err != nil {
		t.Fatalf("responder read message3: %v", err)
	}

	txIni, rxIni, err := ini.Split()
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	txResp, rxResp := resp.Split()

	// initiator -> responder
	ct, err := txIni.EncryptWithAD(nil, []byte("hello responder"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pt, err := rxResp.DecryptWithAD(nil, ct)
	if err != nil {
		t.Fatalf("responder decrypt: %v", err)
	}
	if string(pt) != "hello responder" {
		t.Fatalf("roundtrip mismatch: %q", pt)
	}

	// responder -> initiator
	ct2, err := txResp.EncryptWithAD(nil, []byte("hello initiator"))
	if err != nil {
		t.Fatalf("responder encrypt: %v", err)
	}
	pt2, err := rxIni.DecryptWithAD(nil, ct2)
	if err != nil {
		t.Fatalf("initiator decrypt: %v", err)
	}
	if string(pt2) != "hello initiator" {
		t.Fatalf("reverse roundtrip mismatch: %q", pt2)
	}
}

func TestServiceFrameRoundTrip(t *testing.T) {
	req := ApplicationRequest{
		Verb: "POST",
		Path: "/chat/stream",
		Headers: []Header{
			{Key: "Content-Type", Value: "application/json"},
			{Key: "x-request-id", Value: "abc"},
		},
		Body:    []byte(`{"message":"hi"}`),
		EndBody: true,
	}
	frame, err := decodeServiceFrame(encodeServiceFrameRequest(7, req))
	if err != nil {
		t.Fatalf("decode request frame: %v", err)
	}
	if frame.StreamID != 7 || frame.Kind != "request" {
		t.Fatalf("unexpected frame: %+v", frame)
	}
	// Decode the embedded application request through the service response path.
	appReq, err := parseFields(frame.Request)
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	got := map[int][]byte{}
	for _, f := range appReq {
		if f.wire == 2 {
			got[f.num] = f.bytes
		}
	}
	if string(got[1]) != "POST" || string(got[2]) != "/chat/stream" {
		t.Fatalf("verb/path mismatch: %q %q", got[1], got[2])
	}
	if string(got[4]) != `{"message":"hi"}` {
		t.Fatalf("body mismatch: %q", got[4])
	}

	// Response frame
	respBody := fieldVarint(1, 200)
	respBody = append(respBody, fieldBytes(3, []byte(`{"ok":true}`))...)
	respFrame, err := decodeServiceFrame(append(fieldVarint(1, 7), fieldBytes(3, respBody)...))
	if err != nil {
		t.Fatalf("decode response frame: %v", err)
	}
	if respFrame.Kind != "response" || respFrame.Response == nil || respFrame.Response.Status != 200 {
		t.Fatalf("unexpected response frame: %+v", respFrame)
	}
	if string(respFrame.Response.Body) != `{"ok":true}` {
		t.Fatalf("response body mismatch: %q", respFrame.Response.Body)
	}
}

func TestNoiseChunkReassembly(t *testing.T) {
	payload := bytes.Repeat([]byte{0x7e}, 9<<20) // forces multiple chunks
	parts, err := splitNoisePayload(payload)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(parts))
	}
	r := newReassembler()
	var out []byte
	for i, part := range parts {
		got, complete, err := r.feed(part)
		if err != nil {
			t.Fatalf("feed %d: %v", i, err)
		}
		if i == len(parts)-1 {
			if !complete {
				t.Fatalf("final chunk not complete")
			}
			out = got
		} else if complete {
			t.Fatalf("chunk %d completed early", i)
		}
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("reassembled payload mismatch")
	}
}

func TestServiceRequestEnvelope(t *testing.T) {
	inner := encodeServiceFrameRequest(1, ApplicationRequest{Verb: "GET", Path: "/model", EndBody: true})
	env := encodeServiceRequest(serviceDaemon, inner)
	// ServiceResponse wraps the same payload shape.
	resp := fieldBytes(1, inner)
	got, err := decodeServiceResponse(resp)
	if err != nil {
		t.Fatalf("decode service response: %v", err)
	}
	if !bytes.Equal(got, inner) {
		t.Fatalf("service payload mismatch")
	}
	if len(env) == 0 {
		t.Fatalf("empty envelope")
	}
}
