package refimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake-image")

func TestNormalizeDataURLAndBase64(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	normalized, err := Normalize(context.Background(), dataURL, Policy{})
	if err != nil || normalized.MIME != "image/png" || string(normalized.Data) != string(pngBytes) {
		t.Fatalf("data URL: %+v %v", normalized, err)
	}
	normalized, err = Normalize(context.Background(), base64.StdEncoding.EncodeToString(pngBytes), Policy{})
	if err != nil || normalized.MIME != "image/png" {
		t.Fatalf("base64: %+v %v", normalized, err)
	}
	// Standard base64 uses '/', which must not be mistaken for a file path.
	slashPayload := base64.StdEncoding.EncodeToString([]byte{0xff, 0xff, 0xff})
	if _, err := Normalize(context.Background(), slashPayload, Policy{}); err != nil {
		t.Fatalf("base64 containing slash rejected: %v", err)
	}
}

func TestNormalizeRejectsNonImageAndInvalidEncoding(t *testing.T) {
	text := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hello"))
	if _, err := Normalize(context.Background(), text, Policy{}); !errors.Is(err, ErrNotImage) {
		t.Fatalf("non-image accepted: %v", err)
	}
	if _, err := Normalize(context.Background(), "data:image/png;base64,!!!", Policy{}); err == nil {
		t.Fatal("invalid data URL accepted")
	}
	if _, err := Normalize(context.Background(), "not base64!!!", Policy{}); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}

func TestNormalizeLocalFilePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(context.Background(), path, Policy{}); !errors.Is(err, ErrLocalFileDisabled) {
		t.Fatalf("local file allowed by default: %v", err)
	}
	normalized, err := Normalize(context.Background(), path, Policy{AllowLocalFiles: true})
	if err != nil || normalized.MIME != "image/png" || string(normalized.Data) != string(pngBytes) {
		t.Fatalf("local file failed: %+v %v", normalized, err)
	}
}

func TestNormalizeHTTPPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()

	if _, err := Normalize(context.Background(), server.URL, Policy{}); !errors.Is(err, ErrPrivateNetwork) {
		t.Fatalf("private fetch allowed by default: %v", err)
	}
	normalized, err := Normalize(context.Background(), server.URL, Policy{AllowPrivateNetwork: true})
	if err != nil || normalized.MIME != "image/png" || string(normalized.Data) != string(pngBytes) {
		t.Fatalf("private fetch with opt-in failed: %+v %v", normalized, err)
	}
}

func TestNormalizeAllLimits(t *testing.T) {
	raw := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if _, err := NormalizeAll(context.Background(), []string{raw, raw, raw, raw, raw}, Policy{}); !errors.Is(err, ErrTooManyImages) {
		t.Fatalf("five images accepted: %v", err)
	}
	tooLarge := []byte(strings.Repeat("x", MaxImageBytes+1))
	if _, err := Normalize(context.Background(), base64.StdEncoding.EncodeToString(tooLarge), Policy{}); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("oversized image accepted: %v", err)
	}
	// Three images of roughly 8 MiB exceed the aggregate limit.
	chunk := base64.StdEncoding.EncodeToString(make([]byte, (8<<20)+1))
	big := "data:image/png;base64," + chunk
	if _, err := NormalizeAll(context.Background(), []string{big, big, big}, Policy{}); !errors.Is(err, ErrTotalTooLarge) {
		t.Fatalf("aggregate limit not enforced: %v", err)
	}
}

func randomReference(t *testing.T, width, height int, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewSource(int64(width*height + 1)))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			a := uint8(255)
			if alpha {
				a = uint8(rng.Intn(256))
			}
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: a})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestNormalizeCompressesOversizedReference(t *testing.T) {
	source := randomReference(t, 160, 160, false)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(source)
	normalized, err := Normalize(context.Background(), dataURL, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized.DataURL()) > maxInlineDataURLBytes {
		t.Fatalf("inline reference still %d bytes", len(normalized.DataURL()))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(normalized.Data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 160 || cfg.Height > 160 || cfg.Width <= 0 || cfg.Height <= 0 {
		t.Fatalf("unexpected dimensions %dx%d (format %s)", cfg.Width, cfg.Height, format)
	}
}

func TestNormalizeKeepsTransparencyWhenCompressing(t *testing.T) {
	source := randomReference(t, 160, 160, true)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(source)
	normalized, err := Normalize(context.Background(), dataURL, Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.MIME != "image/png" {
		t.Fatalf("transparent reference re-encoded as %s", normalized.MIME)
	}
	decoded, err := png.Decode(bytes.NewReader(normalized.Data))
	if err != nil {
		t.Fatal(err)
	}
	if !imageHasAlpha(decoded) {
		t.Fatal("alpha channel was lost during compression")
	}
	if len(normalized.DataURL()) > maxInlineDataURLBytes {
		t.Fatalf("inline reference still %d bytes", len(normalized.DataURL()))
	}
}

func TestNormalizeRejectsOversizedUndecodableReference(t *testing.T) {
	invalid := bytes.Repeat([]byte{0x42}, 50_000)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(invalid)
	if _, err := Normalize(context.Background(), dataURL, Policy{}); !errors.Is(err, ErrInlineTooLarge) {
		t.Fatalf("oversized undecodable reference accepted: %v", err)
	}
}
