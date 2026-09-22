package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
	"github.com/nlink-jp/video-studio-mcp/internal/transport"
	"github.com/nlink-jp/video-studio-mcp/internal/workdir"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// Whether a file exists is never the difference between two answers. The
// manifest and the images and audio it names are workspace-relative and read
// through an os.Root, but a place the floor refuses can lie inside a
// workspace: a .env, this server's own directory, or the file a link in ~/.ssh
// leads to when the workspace is in that sync folder. Each case names one path
// twice — once while a file is there and once after it is removed — and the
// whole answer must be the same both times, and a refusal.
//
// The layer observed is the tool call, the answer a caller receives. The home
// directory is a temporary one: nothing is created, read or written in a real
// credential directory (pathguard still lists the account's own, for the links
// inside them).
func TestExistenceIsNotRevealed(t *testing.T) {
	base := realDir(t, t.TempDir())
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	work := filepath.Join(base, "sync")
	ws := filepath.Join(work, "ws")
	server := filepath.Join(ws, "srv") // this server's own directory, inside the workspace
	for _, d := range []string{filepath.Join(home, ".aws"), filepath.Join(home, ".ssh"), ws, server} {
		mkdirAll(t, d)
	}
	symlink(t, filepath.Join(ws, "ssh_config.png"), filepath.Join(home, ".ssh", "config"))
	symlink(t, filepath.Join(home, ".aws", "planted.png"), filepath.Join(ws, "lnk_file.png"))
	symlink(t, filepath.Join(home, ".aws"), filepath.Join(ws, "lnk_dir"))
	writeFileAt(t, filepath.Join(ws, "ok.png"), pngImage)
	writeFileAt(t, filepath.Join(ws, "ok.wav"), "aud")

	srv := serverWithDir(t, server)
	async := false
	answer := func(manifest, page string) string {
		if page != "" {
			writeFileAt(t, filepath.Join(ws, "deck.jsonl"), page+"\n")
		}
		raw, _ := json.Marshal(map[string]any{"work_dir": work, "workspace_id": "ws", "manifest_path": manifest, "async": async})
		_, err := srv.Call(context.Background(), "master", raw)
		if err == nil {
			return "accepted"
		}
		var te *toolerr.Error
		if !errors.As(err, &te) {
			return "untyped: " + err.Error()
		}
		d, _ := json.Marshal(te.Details)
		return fmt.Sprintf("%s | %s | %s", te.Code, te.Message, d)
	}
	for _, c := range []struct{ name, rel, leaf string }{
		{"a .env file", filepath.Join("sub", ".env"), filepath.Join(ws, "sub", ".env")},
		{"this server's own directory", filepath.Join("srv", "config.toml"), filepath.Join(server, "config.toml")},
		{"where a link in ~/.ssh leads", "ssh_config.png", filepath.Join(ws, "ssh_config.png")},
		{"a planted link to a credential file", "lnk_file.png", filepath.Join(home, ".aws", "planted.png")},
		{"through a planted link to a credential directory", filepath.Join("lnk_dir", "via.png"), filepath.Join(home, ".aws", "via.png")},
	} {
		for _, as := range []string{"manifest", "image", "audio", "page 2 image", "async image"} {
			t.Run(as+"/"+c.name, func(t *testing.T) {
				call := func() string {
					async = as == "async image"
					switch as {
					case "image", "async image":
						return answer("deck.jsonl", fmt.Sprintf(`{"image":%q,"audio":"ok.wav"}`, c.rel))
					case "audio":
						return answer("deck.jsonl", fmt.Sprintf(`{"image":"ok.png","audio":%q}`, c.rel))
					case "page 2 image":
						return answer("deck.jsonl", `{"image":"ok.png","audio":"ok.wav"}`+"\n"+fmt.Sprintf(`{"image":%q,"audio":"ok.wav"}`, c.rel))
					}
					return answer(c.rel, "")
				}
				writeFileAt(t, c.leaf, "SECRET=1\n")
				e := call()
				if err := os.Remove(c.leaf); err != nil {
					t.Fatal(err)
				}
				m := call()
				if !strings.HasPrefix(e, toolerr.CodePathNotAllowed+" ") || strings.Contains(e, "SECRET") {
					t.Errorf("existing: %s\n  want path_not_allowed, unread", e)
				}
				if e != m {
					t.Errorf("the answer tells them apart\n  existing: %s\n  missing:  %s", e, m)
				}
			})
		}
	}
	// A page name ffmpeg reads as a pattern is refused on the call, async too.
	async = true
	writeFileAt(t, filepath.Join(ws, "p%d.png"), pngImage)
	if a := answer("deck.jsonl", `{"image":"p%d.png","audio":"ok.wav"}`); !strings.HasPrefix(a, toolerr.CodePathNotAllowed+" ") {
		t.Errorf("an async deck with a pattern name: %s, want path_not_allowed on the call", a)
	}
	// The control: an ordinary deck renders.
	async = false
	if a := answer("deck.jsonl", `{"image":"ok.png","audio":"ok.wav"}`); a != "accepted" {
		t.Errorf("an ordinary deck: %s", a)
	}
}

// serverWithDir is newHarness's server with this server's own directory at dir.
func serverWithDir(t *testing.T, dir string) *mcpserver.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Video.FFmpegPath = "/bin/ls"
	cfg.Video.FFprobePath = "/bin/ls"
	resolver := workdir.NewResolver(dir)
	srv := mcpserver.New("video-studio-mcp", "test",
		transport.NewStdioTransport(strings.NewReader(""), io.Discard), nil)
	Register(srv, &Deps{Cfg: cfg, WS: workspace.NewManager(resolver.CheckBeneath, resolver.LocalPath), WorkDir: resolver, Runner: fakeRunner{}})
	return srv
}

func realDir(t *testing.T, d string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mkdirAll(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func writeFileAt(t *testing.T, p, body string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
