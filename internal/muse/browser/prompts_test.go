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
	if !strings.Contains(prompt, "6 秒") || !strings.Contains(prompt, "9:16") || !strings.Contains(prompt, "海浪") {
		t.Fatalf("unexpected default video prompt: %s", prompt)
	}
	custom := buildVideoPrompt(protocol.VideoRequest{Prompt: "镜头推进", Duration: 8, Resolution: "1080p", ReferenceImage: "abc"})
	if !strings.Contains(custom, "8 秒") || !strings.Contains(custom, "1080p") || !strings.Contains(custom, "第一帧") {
		t.Fatalf("unexpected custom video prompt: %s", custom)
	}
}
