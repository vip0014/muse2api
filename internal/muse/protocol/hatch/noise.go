package hatch

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Noise_XX_25519_AESGCM_SHA256, initiator side, mirroring the muse.ai web
// client's hand-written implementation (XX pattern, SHA-256 HKDF, AES-GCM with
// a 4+8 byte big-endian nonce).
var noiseProtocolName = []byte("Noise_XX_25519_AESGCM_SHA256")

var noiseEmptyAD = []byte{}

func noiseHash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func noiseMAC(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// hkdfNoise mirrors noise.py: temp = HMAC(ck, ikm); out_i = HMAC(temp, prev||i).
func hkdfNoise(ck, ikm []byte, outputs int) [][]byte {
	temp := noiseMAC(ck, ikm)
	out := make([][]byte, outputs)
	prev := []byte{}
	for i := 0; i < outputs; i++ {
		buf := append(append([]byte{}, prev...), byte(i+1))
		out[i] = noiseMAC(temp, buf)
		prev = out[i]
	}
	return out
}

func noiseNonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

// CipherState is one directional AES-256-GCM stream after Split.
type CipherState struct {
	key   []byte
	nonce uint64
}

func (c *CipherState) HasKey() bool { return len(c.key) == 32 }

func (c *CipherState) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *CipherState) EncryptWithAD(ad, plaintext []byte) ([]byte, error) {
	if !c.HasKey() {
		return append([]byte(nil), plaintext...), nil
	}
	aead, err := c.aead()
	if err != nil {
		return nil, err
	}
	out := aead.Seal(nil, noiseNonce(c.nonce), plaintext, ad)
	c.nonce++
	return out, nil
}

func (c *CipherState) DecryptWithAD(ad, ciphertext []byte) ([]byte, error) {
	if !c.HasKey() {
		return append([]byte(nil), ciphertext...), nil
	}
	aead, err := c.aead()
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aead.Overhead() {
		return nil, errors.New("noise: ciphertext too short")
	}
	out, err := aead.Open(nil, noiseNonce(c.nonce), ciphertext, ad)
	if err != nil {
		return nil, err
	}
	c.nonce++
	return out, nil
}

type symmetricState struct {
	h      []byte
	ck     []byte
	cipher *CipherState
}

func newSymmetricState() *symmetricState {
	name := make([]byte, 32)
	copy(name, noiseProtocolName) // InitializeSymmetric: name ljust(32)
	s := &symmetricState{h: name, ck: name, cipher: &CipherState{}}
	s.mixHash(nil) // MixHash(prologue) with empty prologue
	return s
}

func (s *symmetricState) mixHash(data []byte) {
	s.h = noiseHash(s.h, data)
}

func (s *symmetricState) mixKey(ikm []byte) {
	out := hkdfNoise(s.ck, ikm, 2)
	s.ck = out[0]
	s.cipher.key = out[1]
	s.cipher.nonce = 0
}

func (s *symmetricState) encryptAndHash(plaintext []byte) ([]byte, error) {
	ct, err := s.cipher.EncryptWithAD(s.h, plaintext)
	if err != nil {
		return nil, err
	}
	s.mixHash(ct)
	return ct, nil
}

func (s *symmetricState) decryptAndHash(ciphertext []byte) ([]byte, error) {
	pt, err := s.cipher.DecryptWithAD(s.h, ciphertext)
	if err != nil {
		return nil, err
	}
	s.mixHash(ciphertext)
	return pt, nil
}

func (s *symmetricState) split() (*CipherState, *CipherState) {
	out := hkdfNoise(s.ck, nil, 2)
	return &CipherState{key: out[0]}, &CipherState{key: out[1]}
}

func x25519Generate() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

func x25519Public(raw []byte) (*ecdh.PublicKey, error) {
	return ecdh.X25519().NewPublicKey(raw)
}

// ---------------------------------------------------------------------------
// XX initiator
// ---------------------------------------------------------------------------

// NoiseXXInitiator drives the three-message XX handshake from the client side.
type NoiseXXInitiator struct {
	ss           *symmetricState
	ephemeral    *ecdh.PrivateKey
	static       *ecdh.PrivateKey
	remoteEph    []byte
	remoteStatic []byte
	phase        int
}

// NewNoiseXXInitiator returns an initiator ready to WriteMessage1.
func NewNoiseXXInitiator() *NoiseXXInitiator {
	return &NoiseXXInitiator{ss: newSymmetricState(), phase: 1}
}

// WriteMessage1 returns e || encrypt_and_hash(payload).
func (n *NoiseXXInitiator) WriteMessage1(payload []byte) ([]byte, error) {
	if n.phase != 1 {
		return nil, errors.New("noise: write_message1 in wrong phase")
	}
	eph, err := x25519Generate()
	if err != nil {
		return nil, err
	}
	n.ephemeral = eph
	ePub := eph.PublicKey().Bytes()
	n.ss.mixHash(ePub)
	enc, err := n.ss.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}
	n.phase = 2
	out := append([]byte{}, ePub...)
	return append(out, enc...), nil
}

// ReadMessage2 consumes e, ee, s, es and returns the responder payload.
func (n *NoiseXXInitiator) ReadMessage2(message []byte) ([]byte, error) {
	if n.phase != 2 {
		return nil, errors.New("noise: read_message2 in wrong phase")
	}
	if n.ephemeral == nil || len(message) < 96 {
		return nil, fmt.Errorf("noise: message 2 too short (%d < 96)", len(message))
	}
	n.remoteEph = append([]byte(nil), message[0:32]...)
	n.ss.mixHash(n.remoteEph)
	remoteEphPub, err := x25519Public(n.remoteEph)
	if err != nil {
		return nil, err
	}
	dh1, err := n.ephemeral.ECDH(remoteEphPub)
	if err != nil {
		return nil, err
	}
	n.ss.mixKey(dh1)
	staticRaw, err := n.ss.decryptAndHash(message[32:80])
	if err != nil {
		return nil, err
	}
	if len(staticRaw) != 32 {
		return nil, errors.New("noise: message 2 static key is not 32 bytes")
	}
	n.remoteStatic = append([]byte(nil), staticRaw...)
	remoteStaticPub, err := x25519Public(n.remoteStatic)
	if err != nil {
		return nil, err
	}
	dh2, err := n.ephemeral.ECDH(remoteStaticPub)
	if err != nil {
		return nil, err
	}
	n.ss.mixKey(dh2)
	payload, err := n.ss.decryptAndHash(message[80:])
	if err != nil {
		return nil, err
	}
	n.phase = 3
	return payload, nil
}

// WriteMessage3 returns encrypt_and_hash(s) || encrypt_and_hash(payload).
func (n *NoiseXXInitiator) WriteMessage3(payload []byte) ([]byte, error) {
	if n.phase != 3 || n.remoteEph == nil {
		return nil, errors.New("noise: write_message3 in wrong phase")
	}
	st, err := x25519Generate()
	if err != nil {
		return nil, err
	}
	n.static = st
	encStatic, err := n.ss.encryptAndHash(st.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	remoteEphPub, err := x25519Public(n.remoteEph)
	if err != nil {
		return nil, err
	}
	dh, err := st.ECDH(remoteEphPub)
	if err != nil {
		return nil, err
	}
	n.ss.mixKey(dh)
	encPayload, err := n.ss.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}
	n.phase = 4
	out := append([]byte{}, encStatic...)
	return append(out, encPayload...), nil
}

// Split returns (send, receive) cipher states for the initiator.
func (n *NoiseXXInitiator) Split() (*CipherState, *CipherState, error) {
	if n.phase != 4 {
		return nil, nil, errors.New("noise: split in wrong phase")
	}
	n.phase = 5
	tx, rx := n.ss.split()
	return tx, rx, nil
}

// HandshakeHash exposes the current handshake hash (useful for attestation).
func (n *NoiseXXInitiator) HandshakeHash() []byte {
	return append([]byte(nil), n.ss.h...)
}

// RemoteStaticPublicKey returns the responder static public key after
// ReadMessage2.
func (n *NoiseXXInitiator) RemoteStaticPublicKey() []byte {
	return append([]byte(nil), n.remoteStatic...)
}

// ---------------------------------------------------------------------------
// XX responder (test/verification only)
// ---------------------------------------------------------------------------

// noiseXXResponder validates the initiator implementation against the same
// primitives. It is intentionally unexported.
type noiseXXResponder struct {
	ss        *symmetricState
	ephemeral *ecdh.PrivateKey
	static    *ecdh.PrivateKey
	remoteEph []byte
}

func newNoiseXXResponder() *noiseXXResponder {
	st, _ := x25519Generate()
	return &noiseXXResponder{ss: newSymmetricState(), static: st}
}

func (r *noiseXXResponder) ReadMessage1(message []byte) ([]byte, error) {
	if len(message) < 32 {
		return nil, errors.New("noise: message 1 too short")
	}
	r.remoteEph = append([]byte(nil), message[0:32]...)
	r.ss.mixHash(r.remoteEph)
	return r.ss.decryptAndHash(message[32:])
}

func (r *noiseXXResponder) WriteMessage2(payload []byte) ([]byte, error) {
	eph, err := x25519Generate()
	if err != nil {
		return nil, err
	}
	r.ephemeral = eph
	ePub := eph.PublicKey().Bytes()
	r.ss.mixHash(ePub)
	remoteEphPub, err := x25519Public(r.remoteEph)
	if err != nil {
		return nil, err
	}
	dh1, err := eph.ECDH(remoteEphPub)
	if err != nil {
		return nil, err
	}
	r.ss.mixKey(dh1)
	encStatic, err := r.ss.encryptAndHash(r.static.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	dh2, err := r.static.ECDH(remoteEphPub)
	if err != nil {
		return nil, err
	}
	r.ss.mixKey(dh2)
	encPayload, err := r.ss.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}
	out := append([]byte{}, ePub...)
	out = append(out, encStatic...)
	return append(out, encPayload...), nil
}

func (r *noiseXXResponder) ReadMessage3(message []byte) ([]byte, error) {
	if len(message) < 48 {
		return nil, errors.New("noise: message 3 too short")
	}
	staticRaw, err := r.ss.decryptAndHash(message[0:48])
	if err != nil {
		return nil, err
	}
	if len(staticRaw) != 32 {
		return nil, errors.New("noise: initiator static key is not 32 bytes")
	}
	staticPub, err := x25519Public(staticRaw)
	if err != nil {
		return nil, err
	}
	dh, err := r.ephemeral.ECDH(staticPub)
	if err != nil {
		return nil, err
	}
	r.ss.mixKey(dh)
	return r.ss.decryptAndHash(message[48:])
}

// Split returns (send, receive) for the responder: reversed versus initiator.
func (r *noiseXXResponder) Split() (*CipherState, *CipherState) {
	first, second := r.ss.split()
	return second, first
}
