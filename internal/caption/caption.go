// Package caption renders a caption string to a transparent PNG overlay sized
// to the video canvas, using the bundled M PLUS 1p font (SIL OFL 1.1) via
// golang.org/x/image. The overlay is then composited by ffmpeg's core overlay
// filter — this deliberately avoids ffmpeg's drawtext, which requires a
// libfreetype-enabled ffmpeg build that cannot be assumed on user machines.
package caption

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
)

//go:embed fonts/MPLUS1p-Regular.ttf
var fontData []byte

// Render draws text as a bottom-centered caption on a transparent width×height
// RGBA canvas and returns the PNG bytes. The result is meant to be overlaid at
// 0:0 onto the page's video (all positioning is baked into the image). Empty or
// whitespace-only text returns (nil, nil) — the caller skips the overlay.
func Render(cfg config.CaptionConfig, text string, width, height int) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	parsed, err := opentype.Parse(fontData)
	if err != nil {
		return nil, fmt.Errorf("parse bundled font: %w", err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    cfg.FontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("build font face: %w", err)
	}
	defer face.Close()

	metrics := face.Metrics()
	lineH := metrics.Height.Ceil()

	maxTextWidth := width - 2*cfg.MarginH - 2*cfg.BoxPadH
	if maxTextWidth < 1 {
		maxTextWidth = width // pathological config; let it overflow rather than divide-by-zero
	}
	lines := wrapLines(face, text, maxTextWidth)

	widest := 0
	for _, ln := range lines {
		if w := font.MeasureString(face, ln).Ceil(); w > widest {
			widest = w
		}
	}

	boxW := widest + 2*cfg.BoxPadH
	boxH := len(lines)*lineH + 2*cfg.BoxPadV
	boxX := (width - boxW) / 2
	if boxX < 0 {
		boxX = 0
	}
	boxY := height - cfg.MarginV - boxH
	if boxY < 0 {
		boxY = 0
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height)) // fully transparent

	boxCol, err := parseColor(cfg.BoxColor)
	if err != nil {
		return nil, fmt.Errorf("caption.box_color: %w", err)
	}
	boxCol.A = uint8(clamp01(cfg.BoxOpacity) * 255)
	draw.Draw(img, image.Rect(boxX, boxY, boxX+boxW, boxY+boxH), &image.Uniform{C: boxCol}, image.Point{}, draw.Over)

	fontCol, err := parseColor(cfg.FontColor)
	if err != nil {
		return nil, fmt.Errorf("caption.font_color: %w", err)
	}
	drawer := &font.Drawer{Dst: img, Src: image.NewUniform(fontCol), Face: face}
	for i, ln := range lines {
		lw := font.MeasureString(face, ln).Ceil()
		tx := (width - lw) / 2
		ty := boxY + cfg.BoxPadV + i*lineH + metrics.Ascent.Ceil()
		drawer.Dot = fixed.P(tx, ty)
		drawer.DrawString(ln)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode caption png: %w", err)
	}
	return buf.Bytes(), nil
}

// wrapLines greedily wraps text so each line fits within maxWidth. Explicit
// newlines are honored. Wrapping is per-rune (Japanese has no spaces), which
// can split a Latin word mid-token — acceptable for short captions.
func wrapLines(face font.Face, text string, maxWidth int) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		var cur []rune
		for _, r := range para {
			trial := string(append(cur, r))
			if len(cur) > 0 && font.MeasureString(face, trial).Ceil() > maxWidth {
				lines = append(lines, string(cur))
				cur = []rune{r}
			} else {
				cur = append(cur, r)
			}
		}
		if len(cur) > 0 {
			lines = append(lines, string(cur))
		}
	}
	return lines
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// parseColor accepts "#RGB", "#RRGGBB", "#RRGGBBAA", or the names white/black.
func parseColor(s string) (color.RGBA, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "white":
		return color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}, nil
	case "black":
		return color.RGBA{0x00, 0x00, 0x00, 0xFF}, nil
	}
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 { // #RGB -> #RRGGBB
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 && len(h) != 8 {
		return color.RGBA{}, fmt.Errorf("invalid color %q (use #RRGGBB, #RRGGBBAA, white, or black)", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("invalid color %q: %v", s, err)
	}
	if len(h) == 6 {
		return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xFF}, nil
	}
	return color.RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}
