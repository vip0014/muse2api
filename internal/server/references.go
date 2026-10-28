package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"muse2api/internal/apierr"
	"muse2api/internal/refimage"
)

// parseImageReferences returns the raw references in caller order. Legacy
// single-image fields remain supported, but cannot be mixed with `images`.
func parseImageReferences(req imageAPIRequest) ([]string, error) {
	legacy := firstReference(req.ReferenceImage, rawReference(req.Image))
	return parseReferenceList(req.Images, legacy, "images")
}

func parseVideoReferences(req videoAPIRequest) ([]string, error) {
	legacy := firstReference(req.ReferenceImage, rawReference(req.ImageURL), rawReference(req.Image))
	return parseReferenceList(req.Images, legacy, "images")
}

func firstReference(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func parseReferenceList(raw json.RawMessage, legacy, field string) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		if legacy != "" {
			return []string{legacy}, nil
		}
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, apierr.BadRequest(field + " 必须是数组")
	}
	if len(items) > 0 && legacy != "" {
		return nil, apierr.BadRequest(field + " 不能与旧版单图字段同时使用")
	}
	if len(items) > refimage.MaxImages {
		return nil, apierr.BadRequest(refimage.ErrTooManyImages.Error())
	}
	if len(items) == 0 {
		if legacy != "" {
			return []string{legacy}, nil
		}
		return nil, nil
	}
	refs := make([]string, 0, len(items))
	for index, item := range items {
		ref, err := referenceValue(item)
		if err != nil {
			return nil, apierr.BadRequest(fmt.Sprintf("%s[%d] 不是有效图片引用", field, index))
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func referenceValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("empty reference")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text = strings.TrimSpace(text); text == "" {
			return "", fmt.Errorf("empty reference")
		}
		return text, nil
	}
	var object struct {
		URL      json.RawMessage `json:"url"`
		B64      string          `json:"b64_json"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", err
	}
	if value, err := referenceValue(object.ImageURL); err == nil {
		return value, nil
	}
	if value, err := referenceValue(object.URL); err == nil {
		return value, nil
	}
	if object.B64 = strings.TrimSpace(object.B64); object.B64 != "" {
		return "data:image/png;base64," + object.B64, nil
	}
	return "", fmt.Errorf("invalid reference")
}

func (s *Server) prepareReferences(ctx context.Context, raws []string) ([]string, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	policy := refimage.Policy{
		AllowLocalFiles:     s.cfg.RefImageLocalFiles,
		AllowPrivateNetwork: s.cfg.RefImagePrivateFetch,
	}
	refs, err := refimage.NormalizeAll(ctx, raws, policy)
	if err != nil {
		return nil, apierr.BadRequest(err.Error())
	}
	return refs, nil
}

func (s *Server) multipartReferences(r *http.Request) ([]string, error) {
	form := r.MultipartForm
	if form == nil {
		return nil, apierr.BadRequest("multipart 请求无效")
	}
	refs := make([]string, 0, len(form.File["image"])+len(form.Value["image"]))
	total := 0
	for index, header := range form.File["image"] {
		if len(refs) >= refimage.MaxImages {
			return nil, apierr.BadRequest(refimage.ErrTooManyImages.Error())
		}
		file, err := header.Open()
		if err != nil {
			return nil, apierr.BadRequest(fmt.Sprintf("image[%d] 无法读取", index))
		}
		data, readErr := io.ReadAll(io.LimitReader(file, refimage.MaxImageBytes+1))
		_ = file.Close()
		if readErr != nil {
			return nil, apierr.BadRequest(fmt.Sprintf("image[%d] 无法读取", index))
		}
		if len(data) == 0 {
			return nil, apierr.BadRequest(fmt.Sprintf("image[%d] 内容为空", index))
		}
		if len(data) > refimage.MaxImageBytes {
			return nil, apierr.BadRequest(refimage.ErrImageTooLarge.Error())
		}
		contentType := header.Header.Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
			return nil, apierr.BadRequest(fmt.Sprintf("image[%d] 不是图片文件", index))
		}
		total += len(data)
		if total > refimage.MaxTotalBytes {
			return nil, apierr.BadRequest(refimage.ErrTotalTooLarge.Error())
		}
		refs = append(refs, "data:"+contentType+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
	for _, value := range form.Value["image"] {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, apierr.BadRequest(fmt.Sprintf("image[%d] 不是有效图片引用", len(refs)))
		}
		if len(refs) >= refimage.MaxImages {
			return nil, apierr.BadRequest(refimage.ErrTooManyImages.Error())
		}
		if total+len(value) > refimage.MaxTotalBytes {
			return nil, apierr.BadRequest(refimage.ErrTotalTooLarge.Error())
		}
		refs = append(refs, value)
	}
	if len(refs) == 0 {
		return nil, apierr.BadRequest("图像编辑需要参考图片")
	}
	return refs, nil
}
