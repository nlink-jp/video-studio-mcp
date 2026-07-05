// Package master builds a narrated presentation video from a page manifest:
// each page's still image is muxed with its audio into a uniform segment, and
// the segments are concatenated into one MP4 via ffmpeg.
package master

import (
	"bytes"
	"context"
	"os/exec"
)

// Runner executes an external command (ffmpeg / ffprobe). It exists so tests
// can fake the tools; production uses ExecRunner.
type Runner interface {
	Run(ctx context.Context, name string, args []string) (stdout, stderr []byte, exitCode int, err error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	return stdout.Bytes(), stderr.Bytes(), exitCode, err
}
