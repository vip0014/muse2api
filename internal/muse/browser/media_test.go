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

func TestAvatarAnimationIsNeverAResult(t *testing.T) {
	avatar := attachment{TID: "agent-video", HasVideo: true, Src: "/videos/avatars/hatch_making_something.mp4", VSrc: "/videos/avatars/hatch_making_something.mp4", Width: 320, Height: 320}
	if matchingAttachment(avatar, "video") {
		t.Fatal("agent avatar animation was accepted as a video result")
	}
	blobAvatar := attachment{TID: "agent-video", HasVideo: true, Src: "blob:https://muse.ai/1234", VSrc: "blob:https://muse.ai/1234"}
	if got := selectVideoAttachments([]attachment{blobAvatar}); len(got) != 0 {
		t.Fatal("blob video without a presentation container was accepted")
	}
}

func TestSelectVideoAttachmentsPrefersPresentation(t *testing.T) {
	avatar := attachment{TID: "agent-video", HasVideo: true, Src: "https://muse.ai/videos/avatars/hatch_working.mp4", VSrc: "https://muse.ai/videos/avatars/hatch_working.mp4", Width: 320, Height: 320}
	poster := attachment{TID: "hatch-chat-attachment-presentation-video", Presentation: true, HasImage: true, Src: "https://cdn.example/poster.png"}
	real := attachment{TID: "hatch-chat-attachment-presentation-video", Presentation: true, HasVideo: true, Src: "https://cdn.example/poster.png", VSrc: "https://cdn.example/result.mp4"}
	got := selectVideoAttachments([]attachment{avatar, poster, real})
	if len(got) != 1 || mediaSource(got[0], "video") != real.VSrc {
		t.Fatalf("presentation result not selected exclusively: %+v", got)
	}
	// A presentation poster alone must suppress the bubble avatar even before
	// the real video element has a source.
	got = selectVideoAttachments([]attachment{avatar, poster})
	if len(got) != 0 {
		t.Fatalf("avatar leaked while presentation was still loading: %+v", got)
	}
	// Text-delivered direct video links remain a fallback only when the page
	// has no presentation container at all.
	link := attachment{TID: "agent-video", HasVideo: true, Src: "https://cdn.example/clip.mp4", VSrc: "https://cdn.example/clip.mp4"}
	got = selectVideoAttachments([]attachment{avatar, link})
	if len(got) != 1 || mediaSource(got[0], "video") != link.VSrc {
		t.Fatalf("direct video link fallback failed: %+v", got)
	}
}

func TestSaveMediaRejectsUIAsset(t *testing.T) {
	engine := NewEngine(Config{MediaDir: t.TempDir(), MaxMediaBytes: 1024})
	mp4 := []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
	if _, err := engine.saveMedia(mp4, "video/mp4", "https://muse.ai/videos/avatars/hatch_working.mp4", "video"); err == nil {
		t.Fatal("avatar asset was saved as a generation result")
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

func TestRetryableBeforeSubmitOnlyForConnectStage(t *testing.T) {
	if !retryableBeforeSubmit(stageErr(stageConnect, ErrCDPConnectionLost)) {
		t.Fatal("connection failure before submit should be retryable")
	}
	if retryableBeforeSubmit(stageErr(stagePrompt, ErrCDPConnectionLost)) {
		t.Fatal("connection failure after submit must not be retried automatically")
	}
	if retryableBeforeSubmit(stageErr(stageConnect, errors.New("invalid reference image"))) {
		t.Fatal("business error must not be retried")
	}
}
