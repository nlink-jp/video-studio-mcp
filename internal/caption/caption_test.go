package caption

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
)

func TestRenderProducesCanvasSizedPNG(t *testing.T) {
	cfg := config.Default().Caption
	b, err := Render(cfg, "字幕テスト：日本語が焼き込めるか", 1920, 1080)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("empty png")
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != 1920 || img.Bounds().Dy() != 1080 {
		t.Errorf("dims: %v", img.Bounds())
	}
	// The caption sits near the bottom; there must be non-transparent ink there,
	// and the top of the canvas must stay fully transparent.
	if !hasInk(t, b, 1080-200, 1080) {
		t.Error("no caption ink in the bottom band")
	}
	if hasInk(t, b, 0, 200) {
		t.Error("top band should be transparent")
	}
}

func TestRenderEmptyIsNil(t *testing.T) {
	b, err := Render(config.Default().Caption, "   \n  ", 1920, 1080)
	if err != nil || b != nil {
		t.Fatalf("empty caption must yield (nil,nil), got len=%d err=%v", len(b), err)
	}
}

func TestParseColor(t *testing.T) {
	cases := map[string][4]uint8{
		"white":     {255, 255, 255, 255},
		"black":     {0, 0, 0, 255},
		"#FF0000":   {255, 0, 0, 255},
		"#00FF0080": {0, 255, 0, 0x80},
		"#0f0":      {0, 255, 0, 255},
	}
	for in, want := range cases {
		c, err := parseColor(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if [4]uint8{c.R, c.G, c.B, c.A} != want {
			t.Errorf("%s -> %v, want %v", in, [4]uint8{c.R, c.G, c.B, c.A}, want)
		}
	}
	if _, err := parseColor("nope"); err == nil {
		t.Error("expected error for invalid color")
	}
}

// hasInk reports whether any pixel in rows [y0,y1) has non-zero alpha.
func hasInk(t *testing.T, pngBytes []byte, y0, y1 int) bool {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	for y := y0; y < y1 && y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x += 4 { // sample every 4px for speed
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				return true
			}
		}
	}
	return false
}
