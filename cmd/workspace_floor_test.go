package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

// The workspace the server hands every tool judges each read by the floor:
// taken out of the tools.Deps that newToolDeps builds, not a Manager built
// here, so a floor left out of the wiring (or wired as a no-op) fails. The
// judgement itself is tested in internal/workspace.
func TestTheServersWorkspacesJudgeEveryRead(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	work := filepath.Join(home, "project")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	deps := newToolDeps(context.Background(), config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws, err := deps.WS.EnsureUnder(work, "deck1")
	if err != nil {
		t.Fatalf("EnsureUnder: %v", err)
	}
	for name, body := range map[string]string{".env": "TOKEN=x", "deck.jsonl": "{}"} {
		if err := os.WriteFile(ws.Path(name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ws.ReadFile(".env"); !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		t.Errorf("reading .env through the server's workspace: %v, want path_not_allowed", err)
	}
	if _, err := ws.ReadFile("deck.jsonl"); err != nil {
		t.Errorf("an ordinary file: %v", err)
	}
}
