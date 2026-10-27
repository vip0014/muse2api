package browser

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchingAttachmentRejectsVideoPoster(t *testing.T) {
	poster := attachment{TID: "hatch-chat-attachment-presentation-video", Src: "https://cdn.example/poster.png", HasImage: true}
	if matchingAttachment(poster, "video") {
		t.Fatal("video poster without a video source was accepted")
	}
	video := attachment{TID: "hatch-chat-attachment-presentation-video", Src: "https://cdn.example/poster.png", VSrc: "https://cdn.example/movie.mp4", HasVideo: true}
	if !matchingAttachment(video, "video") || mediaSource(video, "video") != video.VSrc {
		t.Fatal("real video attachment was rejected")
	}
}

func TestSaveMediaValidatesKindAndPersists(t *testing.T) {
	engine := NewEngine(Config{MediaDir: t.TempDir(), PublicBase: "http://localhost:18610", MaxMediaBytes: 1024})
	png := []byte("\x89PNG\r\n\x1a\ncontent")
	if _, err := engine.saveMedia(png, "image/png", "poster.png", "video"); err == nil {
		t.Fatal("video request accepted PNG result")
	}
	result, err := engine.saveMedia(png, "image/png", "image.png", "image")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(engine.cfg.MediaDir, result.Filename))
	if err != nil || string(data) != string(png) {
		t.Fatalf("saved media mismatch: %v", err)
	}
	if result.Kind != "image" || result.URL != "http://localhost:18610/v1/media/"+result.Filename {
		t.Fatalf("unexpected media result: %+v", result)
	}
}

func TestNormalizeReference(t *testing.T) {
	payload := []byte("image-data")
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(payload)
	data, mimeType, err := normalizeReference(context.Background(), dataURL)
	if err != nil || mimeType != "image/jpeg" || string(data) != string(payload) {
		t.Fatalf("data URL failed: %q %q %v", data, mimeType, err)
	}
	large := make([]byte, 13<<20)
	data, _, err = normalizeReference(context.Background(), base64.StdEncoding.EncodeToString(large))
	if err != nil || len(data) != len(large) {
		t.Fatalf("valid 13 MiB reference rejected: %d %v", len(data), err)
	}
	if _, _, err := normalizeReference(context.Background(), strings.Repeat("A", (16<<20)*4/3+1025)); err == nil {
		t.Fatal("oversized reference accepted")
	}
}
