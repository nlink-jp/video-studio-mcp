// Package manifest parses the video-studio-mcp page manifest (JSONL): one
// page per line, pairing a still image with its narration audio. It is the
// canonical input contract for the master tool.
package manifest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

// Transition values accepted in a page's transition field. TransitionCut (also
// the zero value) joins the next page with a hard cut; TransitionFade dips to
// the canvas background at the boundary — see ADR-0007.
const (
	TransitionCut  = "cut"
	TransitionFade = "fade"
)

// Page is one slide: a still image shown for the length of its audio, with an
// optional chapter title, caption, and transition into the next page.
type Page struct {
	Image      string `json:"image"`
	Audio      string `json:"audio"`
	Title      string `json:"title,omitempty"` // chapter-marker title; default "Page N"
	Caption    string `json:"caption,omitempty"`
	Transition string `json:"transition,omitempty"`
}

// knownTransitions are accepted transition values; the empty string means the
// default (cut). A page's transition describes the boundary *into the next
// page*, so it is ignored on the last page.
var knownTransitions = map[string]bool{"": true, TransitionCut: true, TransitionFade: true}

// maxReportedErrors bounds how many per-line problems one error reports.
const maxReportedErrors = 20

// Parse decodes a JSONL page manifest. Blank lines and lines beginning with
// '#' are ignored. Every page must carry a non-empty image and audio; unknown
// JSON keys and unknown transitions are rejected so agent typos surface as
// invalid_manifest rather than being silently dropped.
func Parse(data []byte) ([]Page, error) {
	var (
		pages []Page
		errs  []string
	)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		var p Page
		if err := dec.Decode(&p); err != nil {
			errs = appendCapped(errs, fmt.Sprintf("line %d: %v", lineNo, err))
			continue
		}
		if p.Image == "" {
			errs = appendCapped(errs, fmt.Sprintf("line %d: missing \"image\"", lineNo))
		}
		if p.Audio == "" {
			errs = appendCapped(errs, fmt.Sprintf("line %d: missing \"audio\"", lineNo))
		}
		if !knownTransitions[p.Transition] {
			errs = appendCapped(errs, fmt.Sprintf("line %d: unknown transition %q (use \"cut\" or \"fade\")", lineNo, p.Transition))
		}
		pages = append(pages, p)
	}
	if err := sc.Err(); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidManifest, "read manifest: %v", err)
	}
	if len(errs) > 0 {
		return nil, toolerr.New(toolerr.CodeInvalidManifest, "manifest has invalid pages").
			WithDetails(map[string]any{
				"errors":           errs,
				"errors_truncated": len(errs) >= maxReportedErrors,
			})
	}
	if len(pages) == 0 {
		return nil, toolerr.New(toolerr.CodeInvalidManifest, "manifest is empty (no pages)")
	}
	return pages, nil
}

func appendCapped(errs []string, msg string) []string {
	if len(errs) >= maxReportedErrors {
		return errs
	}
	return append(errs, msg)
}
