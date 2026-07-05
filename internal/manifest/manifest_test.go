package manifest

import (
	"errors"
	"strings"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

func TestParseValid(t *testing.T) {
	in := `
# a comment line is ignored
{"image":"p01.png","audio":"p01.wav"}

{"image":"img/p02.png","audio":"aud/p02.wav","caption":"見出し","transition":"fade"}
`
	pages, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages: %d", len(pages))
	}
	if pages[0].Image != "p01.png" || pages[0].Audio != "p01.wav" {
		t.Errorf("page 0: %+v", pages[0])
	}
	if pages[1].Caption != "見出し" || pages[1].Transition != "fade" {
		t.Errorf("page 1: %+v", pages[1])
	}
}

func TestParseTitle(t *testing.T) {
	pages, err := Parse([]byte(`{"image":"p.png","audio":"p.wav","title":"章タイトル"}`))
	if err != nil {
		t.Fatal(err)
	}
	if pages[0].Title != "章タイトル" {
		t.Errorf("title: %q", pages[0].Title)
	}
}

func TestParseMissingFields(t *testing.T) {
	_, err := Parse([]byte(`{"image":"p01.png"}` + "\n" + `{"audio":"p02.wav"}`))
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeInvalidManifest {
		t.Fatalf("want invalid_manifest, got %v", err)
	}
	msgs := te.Details["errors"].([]string)
	if len(msgs) != 2 ||
		!strings.Contains(msgs[0], "missing \"audio\"") ||
		!strings.Contains(msgs[1], "missing \"image\"") {
		t.Errorf("errors: %v", msgs)
	}
}

func TestParseUnknownKey(t *testing.T) {
	_, err := Parse([]byte(`{"image":"p.png","audio":"p.wav","transitoin":"cut"}`))
	if !errors.Is(err, toolerr.New(toolerr.CodeInvalidManifest, "")) {
		t.Fatalf("want invalid_manifest for unknown key, got %v", err)
	}
}

func TestParseUnknownTransition(t *testing.T) {
	_, err := Parse([]byte(`{"image":"p.png","audio":"p.wav","transition":"wipe"}`))
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeInvalidManifest {
		t.Fatalf("want invalid_manifest, got %v", err)
	}
	if !strings.Contains(te.Details["errors"].([]string)[0], "unknown transition") {
		t.Errorf("errors: %v", te.Details["errors"])
	}
}

func TestParseEmpty(t *testing.T) {
	_, err := Parse([]byte("\n  \n# only comments\n"))
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeInvalidManifest {
		t.Fatalf("want invalid_manifest for empty, got %v", err)
	}
	if !strings.Contains(te.Message, "empty") {
		t.Errorf("message: %q", te.Message)
	}
}
