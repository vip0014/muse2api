package protocol

import (
	"strings"
	"testing"
)

func TestBuildImagePromptAppendsExtraLast(t *testing.T) {
	prompt := BuildImagePrompt(ImageRequest{
		Prompt:         "一只猫",
		Size:           "16:9",
		ReferenceImage: "data:image/png;base64,AAAA",
		Extra:          "画面里不要出现任何文字",
	})
	if !strings.HasSuffix(prompt, "，画面里不要出现任何文字") {
		t.Fatalf("extra is not the last instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "16:9 宽屏横屏画幅") {
		t.Fatalf("ratio hint was lost: %q", prompt)
	}
	if strings.Index(prompt, "【纯净画面要求】") > strings.Index(prompt, "画面里不要出现任何文字") {
		t.Fatalf("extra must follow the built-in constraints: %q", prompt)
	}
}

func TestBuildImagePromptOmitsBlankExtra(t *testing.T) {
	prompt := BuildImagePrompt(ImageRequest{Prompt: "一只猫", Extra: "   "})
	if strings.HasSuffix(prompt, "，") || strings.Contains(prompt, "   ") {
		t.Fatalf("blank extra leaked into prompt: %q", prompt)
	}
}
