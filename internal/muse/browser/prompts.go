package browser

import (
	"fmt"
	"strings"

	"muse2api/internal/muse/protocol"
)

func containsAny(value string, keys ...string) bool {
	for _, key := range keys {
		if strings.Contains(value, key) {
			return true
		}
	}
	return false
}

func referenceList(single string, many []string) []string {
	if len(many) > 0 {
		return many
	}
	if strings.TrimSpace(single) != "" {
		return []string{single}
	}
	return nil
}

func buildImagePrompt(request protocol.ImageRequest) string {
	references := referenceList(request.ReferenceImage, request.ReferenceImages)
	hasReference := len(references) > 0
	prompt := strings.TrimSpace(request.Prompt)
	parts := []string{}
	if hasReference {
		if len(references) > 1 {
			parts = append(parts, fmt.Sprintf("基于我本次上传附带的 %d 张有序参考图片进行生图/编辑（第一张作为主体参考，其余图片作为场景、风格或细节参考）：%s", len(references), prompt))
		} else {
			parts = append(parts, "基于我本次上传附带的参考图片进行生图/编辑："+prompt)
		}
	} else {
		parts = append(parts, "全新文生图创作（当前未提供任何参考图，请勿查找历史相册或向用户索要原图，直接根据文字描述从零绘制生成一张全新图片）："+prompt)
	}
	ratio := strings.ToLower(strings.TrimSpace(request.AspectRatio))
	size := strings.ToLower(strings.TrimSpace(request.Size))
	switch {
	case containsAny(ratio+size, "9:16", "9/16", "portrait", "竖屏", "720x1280", "1080x1920"):
		parts = append(parts, "【画面构图与比例要求】：严格 9:16 竖屏满屏画幅（9:16 vertical portrait aspect ratio，高大于宽的手机全屏竖版画面），绝对不要生成横屏，保持垂直构图")
	case containsAny(ratio+size, "16:9", "16/9", "landscape", "横屏", "1280x720", "1920x1080"):
		parts = append(parts, "【画面构图与比例要求】：16:9 宽屏横屏画幅（16:9 widescreen landscape aspect ratio）")
	case containsAny(ratio+size, "1:1", "square", "正方形", "1024x1024"):
		parts = append(parts, "【画面构图与比例要求】：1:1 正方形画幅（1:1 square aspect ratio）")
	case containsAny(ratio+size, "4:3", "4/3"):
		parts = append(parts, "【画面构图与比例要求】：4:3 比例画幅")
	case containsAny(ratio+size, "3:4", "3/4"):
		parts = append(parts, "【画面构图与比例要求】：3:4 竖向画幅")
	case request.AspectRatio != "":
		parts = append(parts, "【画面构图与比例要求】："+request.AspectRatio+" 画面比例")
	case request.Size != "":
		parts = append(parts, "尺寸/比例："+request.Size)
	}
	if hasReference {
		parts = append(parts, "【纯净画面要求】：清除参考图中的文字、水印、签名、角标及 Logo，输出纯净无字画面")
	}
	return strings.Join(parts, "，")
}

func buildVideoPrompt(request protocol.VideoRequest) string {
	prompt := strings.TrimSpace(request.Prompt)
	references := referenceList(request.ReferenceImage, request.ReferenceImages)
	hasReference := len(references) > 0
	ratio := strings.ToLower(strings.TrimSpace(request.AspectRatio + request.Size))
	shape := "动态视频"
	if containsAny(ratio, "9:16", "9/16", "portrait", "竖屏", "720x1280", "1080x1920") {
		shape = "严格 9:16 竖屏手机满屏视频（9:16 vertical portrait video，720x1280，严禁横屏或黑边）"
	} else if containsAny(ratio, "16:9", "16/9", "landscape", "横屏", "1280x720", "1920x1080") {
		shape = "16:9 宽屏横屏视频（16:9 widescreen landscape video）"
	}
	prefix := "全新文生视频创作（纯文本全新生成，严禁参考任何历史图片或上下文）"
	if hasReference {
		if len(references) > 1 {
			prefix = fmt.Sprintf("基于我本次上传的 %d 张有序参考图片生成视频（以第一张作为主要起始画面，其余图片作为主体、场景与风格参考，严禁使用历史图片或其他图像）", len(references))
		} else {
			prefix = "基于我本次上传的参考图片附件作为第一帧参考图（严禁使用历史图片或任何其他图像，必须严格以我当前刚刚上传的这张图片为起始帧）"
		}
	}
	// Muse currently produces one clip of roughly 10 seconds. Duration is not
	// an upstream parameter, so it is deliberately not written into the prompt.
	result := fmt.Sprintf("%s：生成%s：%s", prefix, shape, prompt)
	if request.Resolution != "" {
		result += "，画质规格：" + request.Resolution
	}
	return result
}
