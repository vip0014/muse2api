package browser

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"muse2api/internal/muse/protocol"
)

const mockMusePage = `<!doctype html><html><body data-hatch-shell-hydration-state="hydrated"><main>
<textarea aria-label="Message"></textarea><button aria-label="Send">Send</button>
<script>
document.querySelector('button').addEventListener('click',function(){
  var prompt=document.querySelector('textarea').value;document.querySelector('textarea').value='';
  var bubble=document.createElement('div');bubble.className='hatch-chat-groupable-bubble hatch-agent-bubble-bg';
  document.querySelector('main').appendChild(bubble);
  if(/image|生图/i.test(prompt)){
    var node=document.createElement('div');node.setAttribute('data-testid','hatch-chat-attachment-presentation-image');
    var img=document.createElement('img');img.src='/media.png';node.appendChild(img);bubble.appendChild(node);
  }else{
    bubble.textContent='你';setTimeout(function(){bubble.textContent='你好，';},150);setTimeout(function(){bubble.textContent='你好，Chromium';},300);
  }
});
</script></main></body></html>`

func TestEngineWithRealChromium(t *testing.T) {
	chromium := os.Getenv("MUSE2API_TEST_CHROMIUM")
	if chromium == "" {
		t.Skip("set MUSE2API_TEST_CHROMIUM to run the real Chromium integration test")
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/thread/new":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "refreshed", Path: "/"})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(mockMusePage))
		case "/media.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	root := t.TempDir()
	engine := NewEngine(Config{
		Chromium: chromium, SiteURL: server.URL, PublicBase: "http://localhost",
		ProfileDir: root + "/profile", DownloadDir: root + "/downloads", MediaDir: root + "/media",
		CDPPort: port, LoginWait: 10 * time.Second, ChatTimeout: 15 * time.Second, ImageTimeout: 15 * time.Second,
	})
	defer engine.Close()
	credentials := protocol.AccountCredentials{ID: "mock", Cookies: map[string]string{"session": "initial"}}
	if engine.BrowserRunning() {
		t.Fatal("browser reported running before lazy start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var reply strings.Builder
	updated, err := engine.Chat(ctx, credentials, "hello", func(delta string) error {
		reply.WriteString(delta)
		return nil
	})
	if err != nil {
		if logData, readErr := os.ReadFile(root + "/chromium.log"); readErr == nil {
			t.Logf("Chromium log:\n%s", logData)
		}
		t.Fatal(err)
	}
	if reply.String() != "你好，Chromium" {
		t.Fatalf("unexpected streamed reply %q", reply.String())
	}
	if updated.Cookies["session"] != "refreshed" {
		t.Fatalf("page cookie was not persisted: %+v", updated.Cookies)
	}
	if !engine.BrowserRunning() {
		t.Fatal("browser not reported running after CDP connection")
	}
	result, _, err := engine.Generate(ctx, updated, "image please", "image", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(root + "/media/" + result.Filename)
	if err != nil || len(data) != len(png) {
		t.Fatalf("generated media not saved: size=%d err=%v", len(data), err)
	}
	if result.URL != "http://localhost/v1/media/"+result.Filename {
		t.Fatalf("unexpected media URL %s", result.URL)
	}
}
