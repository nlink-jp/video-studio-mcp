package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/job"
	"github.com/nlink-jp/video-studio-mcp/internal/master"
	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
	"github.com/nlink-jp/video-studio-mcp/internal/transport"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// fakeRunner returns a canned duration for ffprobe and materializes the output
// file for ffmpeg, so tests need neither binary.
type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	for _, a := range args {
		if a == "-show_entries" {
			return []byte("1.000\n"), nil, 0, nil
		}
	}
	out := args[len(args)-1]
	_ = os.WriteFile(out, []byte("x"), 0o644)
	return nil, nil, 0, nil
}

type harness struct {
	t   *testing.T
	srv *mcpserver.Server
	def *workspace.Manager
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := config.Default()
	cfg.Video.FFmpegPath = "/bin/ls"
	cfg.Video.FFprobePath = "/bin/ls"
	cfg.Workspace.Dir = filepath.Join(t.TempDir(), "default")
	wsm := workspace.NewManager(cfg.Workspace.Dir)
	srv := mcpserver.New("video-studio-mcp", "test",
		transport.NewStdioTransport(strings.NewReader(""), io.Discard), nil)
	Register(srv, &Deps{Cfg: cfg, WS: wsm, Runner: fakeRunner{}})
	return &harness{t: t, srv: srv, def: wsm}
}

func (h *harness) call(name string, args map[string]any) (any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.srv.Call(context.Background(), name, raw)
}

// seedDeck prepares an agent-style workspace under a fresh project directory
// and returns its root.
func seedDeck(t *testing.T, wsID string) string {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, wsID)
	for _, d := range []string{"images", "audio"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	man := `{"image":"images/p01.png","audio":"audio/p01.wav"}` + "\n" +
		`{"image":"images/p02.png","audio":"audio/p02.wav"}` + "\n"
	writeFile(t, filepath.Join(base, "deck.jsonl"), man)
	for _, n := range []string{"p01", "p02"} {
		writeFile(t, filepath.Join(base, "images", n+".png"), "img")
		writeFile(t, filepath.Join(base, "audio", n+".wav"), "aud")
	}
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMasterWorkspaceRoot pins the core contract: the server renders in the
// workplace the agent prepared, and leaves the default root untouched.
func TestMasterWorkspaceRoot(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")

	out, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
	})
	if err != nil {
		t.Fatalf("master: %v", err)
	}
	res := out.(master.Result)
	if res.Pages != 2 || res.DurationSeconds < 1.99 || res.DurationSeconds > 2.01 {
		t.Errorf("result: %+v", res)
	}
	// Chapters default to on.
	if res.Chapters != 2 {
		t.Errorf("chapters (default on): %d", res.Chapters)
	}
	if !strings.HasPrefix(res.MasterPath, root) || filepath.Base(res.MasterPath) != "deck.mp4" {
		t.Errorf("master path %q not under workspace_root", res.MasterPath)
	}
	if _, err := os.Stat(res.MasterPath); err != nil {
		t.Errorf("output not written: %v", err)
	}
	// The server-default root must stay untouched.
	if _, err := os.Stat(filepath.Join(h.def.Root(), "deck")); !os.IsNotExist(err) {
		t.Errorf("default root should be untouched, stat err=%v", err)
	}
}

func TestMasterManifestIncomplete(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")
	// Remove page 2's audio.
	if err := os.Remove(filepath.Join(root, "deck", "audio", "p02.wav")); err != nil {
		t.Fatal(err)
	}
	_, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
	})
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeManifestIncomplete {
		t.Fatalf("want manifest_incomplete, got %v", err)
	}
}

func TestMasterSymlinkRejected(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.png")
	writeFile(t, target, "x")
	img := filepath.Join(root, "deck", "images", "p01.png")
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, img); err != nil {
		t.Fatal(err)
	}
	_, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
	})
	if !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		t.Fatalf("want path_not_allowed, got %v", err)
	}
}

func TestMasterInvalidManifest(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")
	writeFile(t, filepath.Join(root, "deck", "bad.jsonl"), `{"image":"a.png","audio":"a.wav","bogus":1}`)
	_, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "bad.jsonl",
	})
	if !errors.Is(err, toolerr.New(toolerr.CodeInvalidManifest, "")) {
		t.Fatalf("want invalid_manifest, got %v", err)
	}
}

func TestMasterMissingArgs(t *testing.T) {
	h := newHarness(t)
	_, err := h.call("master", map[string]any{"workspace_id": "deck"})
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeMissingArgument {
		t.Fatalf("want missing_argument, got %v", err)
	}
}

func TestMasterAsync(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")

	out, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
		"async":          true,
	})
	if err != nil {
		t.Fatalf("master async: %v", err)
	}
	sub := out.(map[string]any)
	if sub["state"] != job.StateRunning || sub["pages"] != 2 {
		t.Fatalf("submit response: %+v", sub)
	}
	jobID, ok := sub["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("no job_id: %+v", sub)
	}

	// Poll check_job until the render finishes (fakeRunner is instant).
	var st job.Status
	for i := 0; i < 500; i++ {
		o, err := h.call("check_job", map[string]any{"job_id": jobID})
		if err != nil {
			t.Fatalf("check_job: %v", err)
		}
		st = o.(job.Status)
		if st.State != job.StateRunning {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if st.State != job.StateDone {
		t.Fatalf("job did not complete: %+v", st)
	}
	res := st.Result.(master.Result)
	if res.Pages != 2 || res.Chapters != 2 {
		t.Errorf("result: %+v", res)
	}
	if _, err := os.Stat(res.MasterPath); err != nil {
		t.Errorf("async output not written: %v", err)
	}
}

func TestCheckJobNotFound(t *testing.T) {
	h := newHarness(t)
	_, err := h.call("check_job", map[string]any{"job_id": "job_deadbeef"})
	if !errors.Is(err, toolerr.New(toolerr.CodeJobNotFound, "")) {
		t.Fatalf("want job_not_found, got %v", err)
	}
}

func TestMasterCanvasOverride(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")
	out, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
		"width":          1080,
		"height":         1920,
		"fps":            24,
	})
	if err != nil {
		t.Fatalf("master: %v", err)
	}
	res := out.(master.Result)
	if res.Width != 1080 || res.Height != 1920 || res.FPS != 24 {
		t.Errorf("canvas override not applied: %+v", res)
	}
}

func TestMasterInvalidOverride(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")
	_, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
		"width":          1081, // odd → rejected before rendering
	})
	var te *toolerr.Error
	if !errors.As(err, &te) || te.Code != toolerr.CodeInvalidArguments {
		t.Fatalf("want invalid_arguments, got %v", err)
	}
}

func TestMasterSoftCaptions(t *testing.T) {
	h := newHarness(t)
	root := seedDeck(t, "deck")
	man := `{"image":"images/p01.png","audio":"audio/p01.wav","caption":"字幕A"}` + "\n" +
		`{"image":"images/p02.png","audio":"audio/p02.wav","caption":"字幕B"}` + "\n"
	writeFile(t, filepath.Join(root, "deck", "deck.jsonl"), man)

	out, err := h.call("master", map[string]any{
		"workspace_id":   "deck",
		"workspace_root": root,
		"manifest_path":  "deck.jsonl",
		"soft_captions":  true,
	})
	if err != nil {
		t.Fatalf("master: %v", err)
	}
	res := out.(master.Result)
	if res.SoftCaptionCues != 2 {
		t.Errorf("soft_caption_cues: %d (want 2)", res.SoftCaptionCues)
	}
	if _, err := os.Stat(res.MasterPath); err != nil {
		t.Errorf("output missing: %v", err)
	}
}

func TestGetUsage(t *testing.T) {
	h := newHarness(t)
	out, err := h.call("get_usage", map[string]any{})
	if err != nil {
		t.Fatalf("get_usage: %v", err)
	}
	raw := out.(mcpserver.RawResult)
	s := raw.Content[0].Text
	for _, want := range []string{"Workspace model", "Render flow", "Error recovery", "manifest"} {
		if !strings.Contains(s, want) {
			t.Errorf("usage missing %q", want)
		}
	}
}

// TestUsageCoherence pins usage.md against the real server surface.
func TestUsageCoherence(t *testing.T) {
	for _, tool := range []string{"master", "check_job"} {
		if !strings.Contains(usageMarkdown, "`"+tool+"`") {
			t.Errorf("usage.md does not reference tool %q", tool)
		}
	}
	for _, code := range []string{
		"invalid_manifest", "manifest_incomplete", "ffmpeg_not_found",
		"ffmpeg_failed", "probe_failed", "caption_failed", "path_not_allowed",
		"invalid_workspace_id", "job_not_found",
	} {
		if !strings.Contains(usageMarkdown, code) {
			t.Errorf("usage.md recovery table missing %q", code)
		}
	}
	for _, field := range []string{`"image"`, `"audio"`, `"title"`, `"caption"`, `"transition"`} {
		if !strings.Contains(usageMarkdown, field) {
			t.Errorf("usage.md manifest schema missing %s", field)
		}
	}
	// Render options an agent can pass; the manual is the only place a client
	// without the workflow skill can learn they exist.
	for _, opt := range []string{"chapters", "captions", "soft_captions", "fade_seconds", "keep_intermediates", "async"} {
		if !strings.Contains(usageMarkdown, opt) {
			t.Errorf("usage.md does not document the %q option", opt)
		}
	}
	if !strings.Contains(Instructions, "get_usage") {
		t.Errorf("initialize instructions must point at get_usage")
	}
}
