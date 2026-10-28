package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"muse2api/internal/refimage"
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
	normalized, err := refimage.Normalize(context.Background(), dataURL, refimage.Policy{})
	if err != nil || normalized.MIME != "image/jpeg" || string(normalized.Data) != string(payload) {
		t.Fatalf("data URL failed: %+v %v", normalized, err)
	}
	large := make([]byte, 10<<20)
	if _, err := refimage.Normalize(context.Background(), base64.StdEncoding.EncodeToString(large), refimage.Policy{}); err != nil {
		t.Fatalf("valid 10 MiB reference rejected: %v", err)
	}
	tooLarge := make([]byte, (10<<20)+1)
	if _, err := refimage.Normalize(context.Background(), base64.StdEncoding.EncodeToString(tooLarge), refimage.Policy{}); !errors.Is(err, refimage.ErrImageTooLarge) {
		t.Fatalf("oversized reference accepted: %v", err)
	}
	if _, err := refimage.Normalize(context.Background(), strings.Repeat("!", 1024), refimage.Policy{}); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}

func TestMediaRefusalText(t *testing.T) {
	if got := mediaRefusalText("视频生成失败：当前地区不支持", "video"); got == "" {
		t.Fatal("video region refusal not detected")
	}
	if got := mediaRefusalText("图片生成失败", "image"); got == "" {
		t.Fatal("image refusal not detected")
	}
	if got := mediaRefusalText("这是一段正常的文字回复", "image"); got != "" {
		t.Fatalf("ordinary text treated as refusal: %q", got)
	}
}
