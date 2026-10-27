package browser

import (
	"strings"
	"testing"
	"time"
)

func TestChatCompletionIdleDecision(t *testing.T) {
	if chatIdleThreshold(false) != chatIdleWithoutStop || chatIdleThreshold(true) != chatIdleAfterStop {
		t.Fatal("unexpected idle thresholds")
	}
	if chatShouldFinish(false, false, chatIdleWithoutStop-time.Millisecond) {
		t.Fatal("finished before idle threshold")
	}
	if !chatShouldFinish(false, false, chatIdleWithoutStop) {
		t.Fatal("did not finish at idle threshold")
	}
	if !chatShouldFinish(false, true, chatIdleAfterStop) {
		t.Fatal("did not finish after stop disappeared")
	}
	if chatShouldFinish(true, true, 10*time.Second) {
		t.Fatal("finished while stop button is visible")
	}
}

func TestDetectMediaType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nrest")
	if kind, _, ext := detectMediaType(png); kind != "image" || ext != ".png" {
		t.Fatalf("png detection failed: %s %s", kind, ext)
	}
	mp4 := []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
	if kind, _, ext := detectMediaType(mp4); kind != "video" || ext != ".mp4" {
		t.Fatalf("mp4 detection failed: %s %s", kind, ext)
	}
	if kind, _, _ := detectMediaType([]byte("<html>not media</html>")); kind != "" {
		t.Fatalf("html accepted as media: %s", kind)
	}
}

func TestSaveMediaRejectsMislabeledContent(t *testing.T) {
	engine := NewEngine(Config{MediaDir: t.TempDir(), MaxMediaBytes: 1024})
	if _, err := engine.saveMedia([]byte("<html>error</html>"), "video/mp4", "x.mp4", "video"); err == nil {
		t.Fatal("html accepted as video")
	}
	if _, err := engine.saveMedia([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, "video/mp4", "x.mp4", "image"); err == nil {
		t.Fatal("video accepted as image")
	}
}

func TestChromiumEnvFiltersSecrets(t *testing.T) {
	t.Setenv("MUSE2API_KEY", "secret-key")
	t.Setenv("MUSE2API_CREDENTIAL_KEY", "secret-credential")
	env := chromiumEnv("/tmp/muse2api-home")
	for _, item := range env {
		if strings.HasPrefix(item, "MUSE2API_KEY=") || strings.HasPrefix(item, "MUSE2API_CREDENTIAL_KEY=") {
			t.Fatalf("secret leaked to Chromium env: %s", item)
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "HOME=/tmp/muse2api-home") {
		t.Fatal("HOME was not set")
	}
}
