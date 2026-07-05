// Package job runs one background render at a time per job and tracks its
// progress in memory.
//
// Jobs are deliberately NOT persisted: after a server restart check_job reports
// job_not_found with recovery guidance, and re-running master is safe because
// it re-renders from the same agent-prepared workspace.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

// Job states.
const (
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// maxFinishedJobs bounds the finished-job history kept in memory.
const maxFinishedJobs = 32

// Progress is a coarse view of how far a render has advanced.
type Progress struct {
	Phase string `json:"phase"` // "starting" | "rendering" | "finalizing" | "done"
	Done  int    `json:"done"`  // pages completed
	Total int    `json:"total"` // pages in the deck
}

// Status is the check_job view of a job.
type Status struct {
	JobID      string         `json:"job_id"`
	State      string         `json:"state"`
	Progress   Progress       `json:"progress"`
	Result     any            `json:"result,omitempty"` // set on done (the master.Result)
	Error      *toolerr.Error `json:"error,omitempty"`  // set on failed
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at,omitempty"`
}

// RunFunc performs the work. It reports progress via report and returns the
// result payload (surfaced verbatim in Status.Result) or an error.
type RunFunc func(ctx context.Context, report func(Progress)) (any, error)

type jobState struct {
	id string

	mu         sync.Mutex
	state      string
	progress   Progress
	result     any
	err        *toolerr.Error
	startedAt  time.Time
	finishedAt time.Time
}

// Manager owns the in-memory job table.
type Manager struct {
	mu   sync.Mutex
	jobs map[string]*jobState
}

// NewManager creates an empty job manager.
func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*jobState)}
}

// Submit starts run in a goroutine and returns the new job id immediately.
// ctx should be the server-lifetime context: cancellation aborts the render
// (and its ffmpeg child) and marks the job failed.
func (m *Manager) Submit(ctx context.Context, total int, run RunFunc) string {
	id := "job_" + randomHex(8)
	js := &jobState{
		id:        id,
		state:     StateRunning,
		progress:  Progress{Phase: "starting", Total: total},
		startedAt: time.Now(),
	}
	m.mu.Lock()
	m.jobs[id] = js
	m.evictLocked()
	m.mu.Unlock()

	go func() {
		res, err := run(ctx, js.report)
		js.finish(res, err)
	}()
	return id
}

func (js *jobState) report(p Progress) {
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.state == StateRunning {
		js.progress = p
	}
}

func (js *jobState) finish(res any, err error) {
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.state != StateRunning {
		return
	}
	js.finishedAt = time.Now()
	if err != nil {
		js.state = StateFailed
		var te *toolerr.Error
		if errors.As(err, &te) {
			js.err = te
		} else {
			js.err = toolerr.New(toolerr.CodeRenderFailed, err.Error())
		}
		return
	}
	js.state = StateDone
	js.result = res
	js.progress.Done = js.progress.Total
	js.progress.Phase = "done"
}

// Get returns the status of a job, or a job_not_found error that tells the
// agent how to recover after a server restart.
func (m *Manager) Get(jobID string) (Status, error) {
	m.mu.Lock()
	js, ok := m.jobs[jobID]
	m.mu.Unlock()
	if !ok {
		return Status{}, toolerr.Newf(toolerr.CodeJobNotFound,
			"job %q not found — jobs do not survive a server restart; re-run master (it re-renders from the same workspace)", jobID)
	}

	js.mu.Lock()
	defer js.mu.Unlock()
	st := Status{
		JobID:     js.id,
		State:     js.state,
		Progress:  js.progress,
		Result:    js.result,
		Error:     js.err,
		StartedAt: js.startedAt.Format(time.RFC3339),
	}
	if !js.finishedAt.IsZero() {
		st.FinishedAt = js.finishedAt.Format(time.RFC3339)
	}
	return st, nil
}

// evictLocked drops the oldest finished jobs beyond maxFinishedJobs.
// Caller holds m.mu.
func (m *Manager) evictLocked() {
	type fin struct {
		id string
		at time.Time
	}
	var finished []fin
	for id, js := range m.jobs {
		js.mu.Lock()
		if js.state != StateRunning {
			finished = append(finished, fin{id, js.finishedAt})
		}
		js.mu.Unlock()
	}
	if len(finished) <= maxFinishedJobs {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].at.Before(finished[j].at) })
	for _, f := range finished[:len(finished)-maxFinishedJobs] {
		delete(m.jobs, f.id)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is unrecoverable in practice; a time-based
		// fallback keeps ids unique enough for an in-memory table.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))[:2*n]
	}
	return hex.EncodeToString(b)
}
