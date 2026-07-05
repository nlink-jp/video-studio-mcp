package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

func waitDone(t *testing.T, m *Manager, id string) Status {
	t.Helper()
	for i := 0; i < 500; i++ {
		st, err := m.Get(id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if st.State != StateRunning {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", id)
	return Status{}
}

func TestSubmitDone(t *testing.T) {
	m := NewManager()
	id := m.Submit(context.Background(), 3, func(ctx context.Context, report func(Progress)) (any, error) {
		report(Progress{Phase: "rendering", Done: 2, Total: 3})
		return map[string]any{"ok": true}, nil
	})
	st := waitDone(t, m, id)
	if st.State != StateDone {
		t.Fatalf("state: %+v", st)
	}
	if st.Progress.Phase != "done" || st.Progress.Done != 3 || st.Progress.Total != 3 {
		t.Errorf("progress: %+v", st.Progress)
	}
	if st.Result.(map[string]any)["ok"] != true {
		t.Errorf("result: %v", st.Result)
	}
	if st.FinishedAt == "" {
		t.Errorf("finishedAt empty")
	}
}

func TestSubmitFailed(t *testing.T) {
	m := NewManager()
	id := m.Submit(context.Background(), 1, func(ctx context.Context, report func(Progress)) (any, error) {
		return nil, toolerr.New(toolerr.CodeFFmpegFailed, "boom")
	})
	st := waitDone(t, m, id)
	if st.State != StateFailed || st.Error == nil || st.Error.Code != toolerr.CodeFFmpegFailed {
		t.Fatalf("status: %+v err=%v", st, st.Error)
	}
	if st.Result != nil {
		t.Errorf("failed job must not carry a result: %v", st.Result)
	}
}

func TestSubmitFailedNonToolerr(t *testing.T) {
	m := NewManager()
	id := m.Submit(context.Background(), 1, func(ctx context.Context, report func(Progress)) (any, error) {
		return nil, errors.New("raw failure")
	})
	st := waitDone(t, m, id)
	if st.State != StateFailed || st.Error.Code != toolerr.CodeRenderFailed {
		t.Fatalf("status: %+v", st)
	}
}

func TestGetNotFound(t *testing.T) {
	m := NewManager()
	_, err := m.Get("job_nope")
	if !errors.Is(err, toolerr.New(toolerr.CodeJobNotFound, "")) {
		t.Fatalf("want job_not_found, got %v", err)
	}
}
