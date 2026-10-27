package browser

import (
	"strings"
	"testing"

	"muse2api/internal/muse/protocol"
)

func TestBuildImagePrompt(t *testing.T) {
	portrait := buildImagePrompt(protocol.ImageRequest{Prompt: "森林", Size: "720x1280"})
	if !strings.Contains(portrait, "9:16") || !strings.Contains(portrait, "森林") {
		t.Fatalf("portrait prompt missing requirements: %s", portrait)
	}
	landscape := buildImagePrompt(protocol.ImageRequest{Prompt: "海岸", AspectRatio: "16:9", ReferenceImage: "data:image/png;base64,AA=="})
	if !strings.Contains(landscape, "16:9") || !strings.Contains(landscape, "参考图片") {
		t.Fatalf("reference prompt missing requirements: %s", landscape)
	}
}

func TestBuildVideoPromptDefaultsAndShape(t *testing.T) {
	prompt := buildVideoPrompt(protocol.VideoRequest{Prompt: "海浪", AspectRatio: "9:16"})
	if strings.Contains(prompt, "秒") || !strings.Contains(prompt, "9:16") || !strings.Contains(prompt, "海浪") {
		t.Fatalf("default prompt must not force a duration: %s", prompt)
	}
	six := buildVideoPrompt(protocol.VideoRequest{Prompt: "镜头推进", Duration: 6, Resolution: "720p", ReferenceImage: "abc"})
	if !strings.Contains(six, "6 秒") || !strings.Contains(six, "720p") || !strings.Contains(six, "第一帧") {
		t.Fatalf("unexpected 6-second prompt: %s", six)
	}
	ten := buildVideoPrompt(protocol.VideoRequest{Prompt: "海岸", Duration: 10})
	if !strings.Contains(ten, "10 秒") {
		t.Fatalf("unexpected 10-second prompt: %s", ten)
	}
}
