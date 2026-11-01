package refimage

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Muse closes the Noise/WebSocket transport when one inline attachment makes
// the chat.stream request too large. Measured against the live gateway: a
// 62 KiB data URL succeeds, while 65 KiB is closed with code 1006 before the
// turn is acknowledged. Keep a safety margin below that 64 KiB boundary.
const (
	maxInlineDataURLBytes = 60 << 10
	maxReferencePixels    = 40_000_000
	inlineStartSide       = 1024
	inlineMinSide         = 64
)

var (
	ErrInlineTooLarge = errors.New("参考图压缩后仍超过上游约 64 KiB 的单图内联上限")
	ErrImagePixels    = errors.New("参考图像素超过 4000 万，拒绝解码")
)

// fitInline keeps every reference within the attachment budget. Small images
// retain their exact bytes; oversized PNG/JPEG/GIF/WebP references are decoded
// and re-encoded (JPEG for opaque images, PNG when transparency must survive)
// with progressively smaller dimensions.
func fitInline(normalized Normalized) (Normalized, error) {
	if len(normalized.DataURL()) <= maxInlineDataURLBytes {
		return normalized, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(normalized.Data))
	if err != nil {
		return Normalized{}, fmt.Errorf("%w: 无法识别图片格式，不能安全压缩", ErrInlineTooLarge)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxReferencePixels {
		return Normalized{}, ErrImagePixels
	}
	img, _, err := image.Decode(bytes.NewReader(normalized.Data))
	if err != nil {
		return Normalized{}, fmt.Errorf("%w: 图片解码失败，不能安全压缩", ErrInlineTooLarge)
	}
	hasAlpha := imageHasAlpha(img)
	side := img.Bounds().Dx()
	if height := img.Bounds().Dy(); height > side {
		side = height
	}
	if side > inlineStartSide {
		side = inlineStartSide
	}
	for side >= inlineMinSide {
		scaled := scaleReference(img, side)
		var encoded bytes.Buffer
		mimeType := "image/png"
		if hasAlpha {
			err = png.Encode(&encoded, scaled)
		} else {
			mimeType = "image/jpeg"
			err = jpeg.Encode(&encoded, scaled, &jpeg.Options{Quality: 85})
		}
		if err != nil {
			return Normalized{}, fmt.Errorf("%w: 图片重编码失败", ErrInlineTooLarge)
		}
		candidate := Normalized{Data: encoded.Bytes(), MIME: mimeType}
		if len(candidate.DataURL()) <= maxInlineDataURLBytes {
			return candidate, nil
		}
		next := side * 4 / 5
		if next >= side {
			break
		}
		side = next
	}
	return Normalized{}, ErrInlineTooLarge
}

func scaleReference(img image.Image, maxSide int) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxSide {
		return img
	}
	scale := float64(maxSide) / float64(longest)
	targetWidth := int(math.Round(float64(width) * scale))
	targetHeight := int(math.Round(float64(height) * scale))
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, xdraw.Src, nil)
	return dst
}

func imageHasAlpha(img image.Image) bool {
	if opaque, ok := img.(interface{ Opaque() bool }); ok {
		return !opaque.Opaque()
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := img.At(x, y).RGBA(); alpha != 0xffff {
				return true
			}
		}
	}
	return false
}
