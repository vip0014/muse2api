package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"muse2api/internal/apierr"
	"muse2api/internal/model"
	"muse2api/internal/muse/protocol"
	"muse2api/internal/storage"
)

var modelAliases = map[string]string{"muse-text": "muse-spark", "muse-chat": "muse-spark", "koda": "muse-spark", "gpt-4o": "muse-spark", "gpt-4.1": "muse-spark", "gpt-5": "muse-spark", "gpt-5-codex": "muse-spark", "claude-sonnet-4": "muse-spark", "deepseek-chat": "muse-spark", "qwen-coder": "muse-spark", "muse-img": "muse-image", "dall-e-3": "muse-image", "gpt-image-1": "muse-image", "muse-vid": "muse-video", "sora": "muse-video", "sora-2": "muse-video", "veo-3": "muse-video"}

func resolveModel(name, def string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return def
	}
	if alias := modelAliases[name]; alias != "" {
		return alias
	}
	return name
}
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	data := []map[string]any{}
	for _, c := range s.driver.Capabilities(r.Context()) {
		if c.ID == "muse-session" {
			continue
		}
		data = append(data, map[string]any{"id": c.ID, "object": "model", "owned_by": "muse", "description": c.Reason, "status": c.Status})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  json.RawMessage `json:"tool_calls"`
}
type chatRequest struct {
	Model         string            `json:"model"`
	Messages      []chatMessage     `json:"messages"`
	Stream        bool              `json:"stream"`
	Prompt        string            `json:"prompt"`
	Timeout       int               `json:"timeout"`
	Tools         []json.RawMessage `json:"tools"`
	StreamOptions json.RawMessage   `json:"stream_options"`
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		out := []string{}
		for _, p := range parts {
			if p.Type == "image_url" {
				out = append(out, "[图片]")
			} else if p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}
func buildPrompt(messages []chatMessage) string {
	systems := []string{}
	turns := []string{}
	for _, m := range messages {
		text := strings.TrimSpace(contentText(m.Content))
		if text == "" {
			continue
		}
		switch strings.ToLower(m.Role) {
		case "system", "developer":
			systems = append(systems, text)
		case "assistant":
			turns = append(turns, "助手：\n"+text)
		case "tool":
			turns = append(turns, "用户：\n【工具执行结果】\n"+text)
		default:
			turns = append(turns, "用户：\n"+text)
		}
	}
	if len(turns) == 1 && len(systems) == 0 && strings.EqualFold(messages[0].Role, "user") {
		return strings.TrimPrefix(turns[0], "用户：\n")
	}
	parts := []string{}
	if len(systems) > 0 {
		parts = append(parts, "背景与任务设定：\n"+strings.Join(systems, "\n\n"))
	}
	parts = append(parts, turns...)
	return strings.Join(parts, "\n\n")
}
func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.Write(w, r, apierr.BadRequest("请求 JSON 无效"))
		return
	}
	if len(req.Tools) > 0 {
		apierr.Write(w, r, apierr.New(400, "capability_unavailable", "原生工具调用未验证；请移除 tools 后重试"))
		return
	}
	prompt := buildPrompt(req.Messages)
	if prompt == "" {
		prompt = strings.TrimSpace(req.Prompt)
	}
	if prompt == "" {
		apierr.Write(w, r, apierr.BadRequest("messages 为空"))
		return
	}
	s.handleChat(w, r, prompt, resolveModel(req.Model, "muse-spark"), req.Stream, false)
}
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, prompt, modelName string, stream, responses bool) {
	ctx := r.Context()
	taskRecord, err := s.store.CreateTask("chat", prompt, prompt, "", "")
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	started := time.Now()
	if _, err := s.store.UpdateTask(taskRecord.ID, func(t *model.Task) error {
		t.Status = model.TaskRunning
		t.StartedAt = started.Unix()
		return nil
	}); err != nil {
		apierr.Write(w, r, err)
		return
	}
	finish := func(status model.TaskStatus, runErr error) {
		if _, err := s.store.UpdateTask(taskRecord.ID, func(t *model.Task) error {
			t.Status = status
			t.CompletedAt = time.Now().Unix()
			t.Elapsed = time.Since(started).Seconds()
			if runErr != nil {
				t.Error = runErr.Error()
			}
			if status == model.TaskSucceeded {
				t.Progress = 100
			}
			return nil
		}); err != nil {
			s.store.AddEvent(model.Event{Level: "error", Type: "task.persist", Message: "对话任务状态持久化失败", RequestID: requestIDFrom(r.Context()), TaskID: taskRecord.ID})
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ChatTimeout)
	defer cancel()
	lease, err := s.pool.Acquire(ctx)
	if err != nil {
		finish(model.TaskFailed, err)
		apierr.Write(w, r, apierr.New(http.StatusServiceUnavailable, "account_unavailable", "没有可用上游账号或账号并发已满"))
		return
	}
	defer lease.Release()
	_, _ = s.store.UpdateTask(taskRecord.ID, func(t *model.Task) error { t.AccountID = lease.Account.ID; return nil })
	events, err := s.driver.Chat(ctx, protocol.ChatRequest{Prompt: prompt, Model: modelName, AccountID: lease.Account.ID})
	if err != nil {
		finish(model.TaskFailed, err)
		apierr.Write(w, r, mapDriverError(err))
		return
	}
	id := newRequestID()
	created := time.Now().Unix()
	if stream {
		s.streamChat(w, r, events, id, created, modelName, responses, taskRecord.ID)
		return
	}
	text, err := collectChat(ctx, events)
	if err != nil {
		finish(model.TaskFailed, err)
		apierr.Write(w, r, mapDriverError(err))
		return
	}
	finish(model.TaskSucceeded, nil)
	if responses {
		writeJSON(w, 200, responseEnvelope(id, created, modelName, "completed", text))
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": created, "model": modelName, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": text}}}, "usage": zeroUsage()})
}
func collectChat(ctx context.Context, events <-chan protocol.ChatEvent) (string, error) {
	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case e, ok := <-events:
			if !ok {
				return b.String(), nil
			}
			if e.Err != nil {
				return "", e.Err
			}
			b.WriteString(e.Delta)
			if e.Done {
				return b.String(), nil
			}
		}
	}
}
func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, events <-chan protocol.ChatEvent, id string, created int64, modelName string, responses bool, taskID string) {
	outcome, outcomeErr := model.TaskCancelled, error(nil)
	defer func() {
		if _, err := s.store.UpdateTask(taskID, func(t *model.Task) error {
			t.Status = outcome
			t.CompletedAt = time.Now().Unix()
			if t.StartedAt > 0 {
				t.Elapsed = time.Since(time.Unix(t.StartedAt, 0)).Seconds()
			}
			if outcomeErr != nil {
				t.Error = outcomeErr.Error()
			}
			if outcome == model.TaskSucceeded {
				t.Progress = 100
			}
			return nil
		}); err != nil {
			s.store.AddEvent(model.Event{Level: "error", Type: "task.persist", Message: "流式对话任务状态持久化失败", RequestID: requestIDFrom(r.Context()), TaskID: taskID})
		}
	}()
	flusher, ok := w.(http.Flusher)
	if !ok {
		outcome, outcomeErr = model.TaskFailed, errors.New("streaming unsupported")
		apierr.Write(w, r, outcomeErr)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	send := func(name string, v any) {
		if name != "" {
			fmt.Fprintf(w, "event: %s\n", name)
		}
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	if responses {
		send("response.created", map[string]any{"type": "response.created", "response": responseEnvelope(id, created, modelName, "in_progress", "")})
	} else {
		send("", chatChunk(id, created, modelName, map[string]any{"role": "assistant"}, nil))
	}
	full := ""
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-events:
			if !ok {
				ok = false
			}
			if e.Err != nil {
				outcome, outcomeErr = model.TaskFailed, e.Err
				if responses {
					send("response.failed", map[string]any{"type": "response.failed", "response": responseEnvelope(id, created, modelName, "failed", full)})
				} else {
					send("", map[string]any{"error": map[string]any{"message": mapDriverError(e.Err).Error(), "type": "server_error"}})
				}
				return
			}
			if e.Delta != "" {
				full += e.Delta
				if responses {
					send("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_" + id, "output_index": 0, "content_index": 0, "delta": e.Delta})
				} else {
					send("", chatChunk(id, created, modelName, map[string]any{"content": e.Delta}, nil))
				}
			}
			if e.Done || !ok {
				outcome = model.TaskSucceeded
				if responses {
					send("response.completed", map[string]any{"type": "response.completed", "response": responseEnvelope(id, created, modelName, "completed", full)})
				} else {
					finish := "stop"
					send("", chatChunk(id, created, modelName, map[string]any{}, &finish))
					fmt.Fprint(w, "data: [DONE]\n\n")
					flusher.Flush()
				}
				return
			}
		}
	}
}
func chatChunk(id string, created int64, modelName string, delta map[string]any, finish *string) map[string]any {
	return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": modelName, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}
func zeroUsage() map[string]int {
	return map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}
func responseEnvelope(id string, created int64, modelName, status, text string) map[string]any {
	content := []any{}
	if text != "" {
		content = append(content, map[string]any{"type": "output_text", "text": text, "annotations": []any{}})
	}
	return map[string]any{"id": id, "object": "response", "created_at": created, "status": status, "model": modelName, "output": []any{map[string]any{"type": "message", "id": "msg_" + id, "role": "assistant", "status": status, "content": content}}, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}}
}
func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model        string            `json:"model"`
		Input        json.RawMessage   `json:"input"`
		Instructions string            `json:"instructions"`
		Stream       bool              `json:"stream"`
		Tools        []json.RawMessage `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.Write(w, r, apierr.BadRequest("请求 JSON 无效"))
		return
	}
	if len(req.Tools) > 0 {
		apierr.Write(w, r, apierr.New(400, "capability_unavailable", "Responses 工具调用未验证"))
		return
	}
	messages := []chatMessage{}
	if req.Instructions != "" {
		messages = append(messages, chatMessage{Role: "system", Content: mustJSON(req.Instructions)})
	}
	var inputText string
	if json.Unmarshal(req.Input, &inputText) == nil {
		messages = append(messages, chatMessage{Role: "user", Content: mustJSON(inputText)})
	} else {
		var inputs []chatMessage
		if json.Unmarshal(req.Input, &inputs) == nil {
			messages = append(messages, inputs...)
		} else {
			apierr.Write(w, r, apierr.BadRequest("input 格式不受支持"))
			return
		}
	}
	prompt := buildPrompt(messages)
	if prompt == "" {
		apierr.Write(w, r, apierr.BadRequest("input 为空"))
		return
	}
	s.handleChat(w, r, prompt, resolveModel(req.Model, "muse-spark"), req.Stream, true)
}
func mustJSON(v string) json.RawMessage { b, _ := json.Marshal(v); return b }

type imageAPIRequest struct {
	Prompt          string          `json:"prompt"`
	Model           string          `json:"model"`
	N               int             `json:"n"`
	Size            string          `json:"size"`
	AspectRatio     string          `json:"aspect_ratio"`
	ResponseFormat  string          `json:"response_format"`
	Timeout         int             `json:"timeout"`
	Extra           string          `json:"extra"`
	Image           json.RawMessage `json:"image"`
	Images          json.RawMessage `json:"images"`
	ReferenceImage  string          `json:"reference_image"`
	ReferenceImages []string        `json:"-"`
	Async           bool            `json:"async"`
}

func (r imageAPIRequest) reference() string {
	if len(r.ReferenceImages) > 0 {
		return r.ReferenceImages[0]
	}
	if r.ReferenceImage != "" {
		return r.ReferenceImage
	}
	return rawReference(r.Image)
}
func rawReference(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var obj struct {
		URL      json.RawMessage `json:"url"`
		B64      string          `json:"b64_json"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if value := rawReference(obj.ImageURL); value != "" {
			return value
		}
		if value := rawReference(obj.URL); value != "" {
			return value
		}
		if strings.TrimSpace(obj.B64) != "" {
			return "data:image/png;base64," + strings.TrimSpace(obj.B64)
		}
	}
	return ""
}
func validateImage(req *imageAPIRequest) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return apierr.BadRequest("prompt 不能为空")
	}
	if req.N > 1 {
		return apierr.New(400, "capability_unavailable", "上游单任务多图能力未验证")
	}
	if req.ResponseFormat == "" {
		req.ResponseFormat = "url"
	}
	if req.ResponseFormat != "url" && req.ResponseFormat != "b64_json" {
		return apierr.BadRequest("response_format 仅支持 url 或 b64_json")
	}
	return nil
}

// generateImageSync is the shared lease/driver/response path for synchronous
// generation and editing. Both endpoints must keep identical timeout, account
// selection, and error mapping behavior.
func (s *Server) generateImageSync(w http.ResponseWriter, r *http.Request, req imageAPIRequest, edit bool) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ImageTimeout)
	defer cancel()
	lease, err := s.pool.Acquire(ctx)
	if err != nil {
		apierr.Write(w, r, apierr.New(http.StatusServiceUnavailable, "account_unavailable", "没有可用上游账号或账号并发已满"))
		return
	}
	defer lease.Release()
	request := protocol.ImageRequest{
		Prompt: req.Prompt, Model: resolveModel(req.Model, "muse-image"),
		Size: req.Size, AspectRatio: req.AspectRatio,
		ReferenceImage: req.reference(), ReferenceImages: req.ReferenceImages,
		ResponseFormat: req.ResponseFormat, AccountID: lease.Account.ID,
	}
	var res *protocol.ImageResult
	if edit {
		res, err = s.driver.EditImage(ctx, request)
	} else {
		res, err = s.driver.GenerateImage(ctx, request)
	}
	if err != nil {
		apierr.Write(w, r, mapDriverError(err))
		return
	}
	resp, err := s.imageResponse(req, res)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) imageGenerations(w http.ResponseWriter, r *http.Request) {
	var req imageAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.Write(w, r, apierr.BadRequest("请求 JSON 无效"))
		return
	}
	if err := validateImage(&req); err != nil {
		apierr.Write(w, r, err)
		return
	}
	rawRefs, err := parseImageReferences(req)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	refs, err := s.prepareReferences(r.Context(), rawRefs)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	req.ReferenceImages = refs
	if req.Async {
		s.queueImage(w, r, req)
		return
	}
	s.generateImageSync(w, r, req, false)
}
func (s *Server) imageEdits(w http.ResponseWriter, r *http.Request) {
	req := imageAPIRequest{Prompt: "参考此图片并进行生图创作", ResponseFormat: "url"}
	var rawRefs []string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(s.cfg.MaxUploadBytes); err != nil {
			apierr.Write(w, r, apierr.BadRequest("multipart 请求无效或过大"))
			return
		}
		req.Prompt = r.FormValue("prompt")
		req.Model = r.FormValue("model")
		req.Size = r.FormValue("size")
		req.AspectRatio = r.FormValue("aspect_ratio")
		req.ResponseFormat = r.FormValue("response_format")
		req.Async = r.FormValue("async") == "true" || r.FormValue("async") == "1"
		var err error
		rawRefs, err = s.multipartReferences(r)
		if err != nil {
			apierr.Write(w, r, err)
			return
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apierr.Write(w, r, apierr.BadRequest("请求 JSON 无效"))
			return
		}
		var err error
		rawRefs, err = parseImageReferences(req)
		if err != nil {
			apierr.Write(w, r, err)
			return
		}
	}
	if strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = "参考此图片并进行生图创作"
	}
	if err := validateImage(&req); err != nil {
		apierr.Write(w, r, err)
		return
	}
	refs, err := s.prepareReferences(r.Context(), rawRefs)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	if len(refs) == 0 {
		apierr.Write(w, r, apierr.BadRequest("图像编辑需要参考图片"))
		return
	}
	req.ReferenceImages = refs
	if req.Async {
		s.queueImage(w, r, req)
		return
	}
	s.generateImageSync(w, r, req, true)
}
func (s *Server) imageTasks(w http.ResponseWriter, r *http.Request) {
	var req imageAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.Write(w, r, apierr.BadRequest("请求 JSON 无效"))
		return
	}
	if err := validateImage(&req); err != nil {
		apierr.Write(w, r, err)
		return
	}
	rawRefs, err := parseImageReferences(req)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	refs, err := s.prepareReferences(r.Context(), rawRefs)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	req.ReferenceImages = refs
	s.queueImage(w, r, req)
}
func (s *Server) queueImage(w http.ResponseWriter, r *http.Request, req imageAPIRequest) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 256 {
		apierr.Write(w, r, apierr.BadRequest("Idempotency-Key 过长"))
		return
	}
	keyHash := ""
	if key != "" {
		sum := sha256.Sum256([]byte(key))
		keyHash = hex.EncodeToString(sum[:])
	}
	payload, _ := json.Marshal(struct {
		Req        imageAPIRequest `json:"request"`
		References []string        `json:"references"`
	}{req, req.ReferenceImages})
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	taskRecord, created, err := s.store.FindOrCreateTask(model.Task{
		Kind: "image", Prompt: req.Prompt, APIPrompt: req.Prompt,
		Model: resolveModel(req.Model, "muse-image"), Size: req.Size, AspectRatio: req.AspectRatio,
		ReferenceImage: req.reference(), ReferenceImages: req.ReferenceImages, ResponseFormat: req.ResponseFormat,
		IdempotencyKey: keyHash, RequestHash: hash,
	})
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			apierr.Write(w, r, apierr.New(409, "idempotency_error", "同一 Idempotency-Key 对应了不同请求"))
			return
		}
		apierr.Write(w, r, err)
		return
	}
	if !created {
		writeJSON(w, 202, legacyTask(taskRecord))
		return
	}
	if err := s.tasks.Enqueue(taskRecord.ID); err != nil {
		_ = s.store.DeleteTask(taskRecord.ID)
		apierr.Write(w, r, err)
		return
	}
	writeJSON(w, 202, map[string]any{"id": taskRecord.ID, "task_id": taskRecord.ID, "object": "image.task", "status": "queued", "progress": 0, "created_at": taskRecord.CreatedAt})
}
func (s *Server) getImageTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.PathValue("id"))
	if err != nil || t.Kind != "image" {
		apierr.Write(w, r, apierr.NotFound("image task 不存在"))
		return
	}
	out := legacyTask(t)
	if t.Status == model.TaskSucceeded && t.Result != nil {
		res := &protocol.ImageResult{URL: t.URL}
		res.Filename, _ = t.Result["filename"].(string)
		res.Kind, _ = t.Result["kind"].(string)
		req := imageAPIRequest{Prompt: t.Prompt, Size: t.Size, ResponseFormat: t.ResponseFormat}
		resp, err := s.imageResponse(req, res)
		if err != nil {
			apierr.Write(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"task": out, "id": t.ID, "task_id": t.ID, "status": out.Status, "progress": 100, "created_at": t.CreatedAt, "data": resp["data"]})
		return
	}
	writeJSON(w, 200, out)
}

const maxBase64MediaBytes = 64 << 20

func (s *Server) imageResponse(req imageAPIRequest, res *protocol.ImageResult) (map[string]any, error) {
	item := map[string]any{"revised_prompt": req.Prompt, "size": req.Size, "kind": res.Kind, "bytes": res.Bytes}
	if req.Size == "" {
		item["size"] = "auto"
	}
	if req.ResponseFormat == "b64_json" {
		if !storage.SafeFilename(res.Filename) {
			return nil, errors.New("媒体文件名无效")
		}
		path := filepath.Join(s.cfg.MediaDir(), res.Filename)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("媒体文件读取失败")
		}
		// Base64 JSON grows by 4/3 in memory on both sides. Refusing an
		// unreasonable payload is safer than exhausting the process.
		if info.Size() > maxBase64MediaBytes {
			return nil, apierr.New(http.StatusRequestEntityTooLarge, "invalid_request_error", "媒体文件过大，无法以 b64_json 返回")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("媒体文件读取失败")
		}
		item["b64_json"] = base64.StdEncoding.EncodeToString(b)
	} else {
		item["url"] = res.URL
	}
	return map[string]any{"created": time.Now().Unix(), "data": []any{item}}, nil
}

type videoAPIRequest struct {
	Prompt          string          `json:"prompt"`
	Model           string          `json:"model"`
	Duration        int             `json:"duration"`
	Size            string          `json:"size"`
	AspectRatio     string          `json:"aspect_ratio"`
	Resolution      string          `json:"resolution"`
	ReferenceImage  string          `json:"reference_image"`
	Image           json.RawMessage `json:"image"`
	ImageURL        json.RawMessage `json:"image_url"`
	Images          json.RawMessage `json:"images"`
	ReferenceImages []string        `json:"-"`
}

// Muse currently exposes one roughly 10-second clip per media task and has no
// duration parameter. Keep 1..10 as request metadata for compatibility, but do
// not let it change the prompt. Longer videos require multiple clips and are
// not implemented by this single-attachment driver.
func normalizeVideoDuration(duration int) (int, error) {
	if duration <= 0 {
		return 0, nil
	}
	if duration <= 10 {
		return duration, nil
	}
	return 0, apierr.BadRequest("本项目只生成单片段（约 10 秒）；更长视频请分段生成")
}

func (s *Server) createVideo(w http.ResponseWriter, r *http.Request) {
	var req videoAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		apierr.Write(w, r, apierr.BadRequest("视频 prompt 不能为空且 JSON 必须有效"))
		return
	}
	normalizedDuration, durationErr := normalizeVideoDuration(req.Duration)
	if durationErr != nil {
		apierr.Write(w, r, durationErr)
		return
	}
	req.Duration = normalizedDuration
	rawRefs, err := parseVideoReferences(req)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	refs, err := s.prepareReferences(r.Context(), rawRefs)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	req.ReferenceImages = refs
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) > 256 {
		apierr.Write(w, r, apierr.BadRequest("Idempotency-Key 过长"))
		return
	}
	keyHash := ""
	if idempotencyKey != "" {
		// Namespace video keys so reusing the same header value for an image
		// task cannot return the wrong task type.
		sum := sha256.Sum256([]byte("video:" + idempotencyKey))
		keyHash = hex.EncodeToString(sum[:])
	}
	payload, err := json.Marshal(struct {
		Req        videoAPIRequest `json:"request"`
		References []string        `json:"references"`
	}{req, req.ReferenceImages})
	if err != nil {
		apierr.Write(w, r, apierr.BadRequest("视频请求无法规范化"))
		return
	}
	sum := sha256.Sum256(payload)
	requestHash := hex.EncodeToString(sum[:])
	taskRecord, created, err := s.store.FindOrCreateTask(model.Task{
		Kind: "video", Prompt: req.Prompt, APIPrompt: req.Prompt,
		Model: resolveModel(req.Model, "muse-video"), Duration: req.Duration,
		Size: req.Size, AspectRatio: req.AspectRatio, Resolution: req.Resolution,
		ReferenceImage: firstReference(req.ReferenceImages...), ReferenceImages: req.ReferenceImages,
		IdempotencyKey: keyHash, RequestHash: requestHash,
	})
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			apierr.Write(w, r, apierr.New(409, "idempotency_error", "同一 Idempotency-Key 对应了不同请求"))
			return
		}
		apierr.Write(w, r, err)
		return
	}
	if !created {
		// Returning the existing task is what makes client retries safe:
		// no second upstream generation is queued.
		writeJSON(w, http.StatusAccepted, legacyTask(taskRecord))
		return
	}
	if err := s.tasks.Enqueue(taskRecord.ID); err != nil {
		_ = s.store.DeleteTask(taskRecord.ID)
		apierr.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": taskRecord.ID, "task_id": taskRecord.ID, "object": "video.task", "status": "queued", "progress": 0, "created_at": taskRecord.CreatedAt})
}
func (s *Server) getVideo(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.PathValue("id"))
	if err != nil || t.Kind != "video" {
		apierr.Write(w, r, apierr.NotFound("task 不存在"))
		return
	}
	writeJSON(w, 200, legacyTask(t))
}
func (s *Server) getMedia(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeMediaName(name) {
		apierr.Write(w, r, apierr.BadRequest("非法文件名"))
		return
	}
	path := filepath.Join(s.cfg.MediaDir(), name)
	f, err := os.Open(path)
	if err != nil {
		apierr.Write(w, r, apierr.NotFound("文件不存在"))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		apierr.Write(w, r, apierr.NotFound("文件不存在"))
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(name))
	if s.cfg.PublicMedia {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	} else {
		// The browser may cache, but must revalidate through authentication
		// before reusing protected media after a session is closed.
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}
func mapDriverError(err error) error {
	if errors.Is(err, protocol.ErrCapabilityUnavailable) {
		return apierr.Capability(err.Error())
	}
	if errors.Is(err, protocol.ErrSessionExpired) {
		return apierr.New(401, "auth_error", "上游 Session 已失效")
	}
	var refusal *protocol.ModelRefusalError
	if errors.As(err, &refusal) {
		return apierr.New(http.StatusBadGateway, "upstream_refusal", "上游拒绝生成媒体："+refusal.Message)
	}
	var up *protocol.UpstreamError
	if errors.As(err, &up) {
		status := 502
		if up.Status == 429 {
			status = 429
		}
		return apierr.New(status, "upstream_error", safeUpstreamError(err))
	}
	return apierr.New(502, "upstream_error", "上游请求失败")
}
