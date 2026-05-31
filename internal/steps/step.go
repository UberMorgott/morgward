// Package steps implements each runbook block (A1..A10) as a discrete Step.
// Steps are stateless: they read facts/config from the Context, run remote
// command blocks via the SSH executor, and report OK / SKIP / FAIL.
package steps

import (
	"encoding/base64"
	"fmt"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/detect"
	"github.com/UberMorgott/morgward/internal/sshx"
	"github.com/UberMorgott/morgward/internal/state"
	"github.com/UberMorgott/morgward/internal/ui"
)

// Status is the tri-state result of a step.
type Status int

const (
	StatusOK Status = iota
	StatusSkip
	StatusFail
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusSkip:
		return "SKIP"
	default:
		return "FAIL"
	}
}

// BenchResult carries the §A4 internet throughput benchmark (PRE vs POST tuning)
// out of the step so the engine can surface it in the final run summary. OK is
// true only when both samples were valid (a comparable PRE→POST pair); when false
// the renderers omit the bench line entirely.
type BenchResult struct {
	PreMBs   float64 // pre-tuning median throughput, MB/s
	PostMBs  float64 // post-tuning median throughput, MB/s
	Ratio    float64 // PostMBs / PreMBs
	OK       bool    // true ⇒ valid pair measured AND tuning kept
	Reverted bool    // true ⇒ tuning regressed throughput and was rolled back
}

// Context carries everything a step needs. The SSH client is shared and may be
// reconnected (A8 reboot) or have its identity switched (A2 strict handoff).
type Context struct {
	Cli      *sshx.Client
	Log      *ui.Logger
	Cfg      *config.Config
	State    *state.Checkpoint
	Facts    *detect.Facts
	AuthLine string // public key authorized_keys line to install for the admin user
	KeyPEM   []byte // private key PEM (for [LOCAL] second-session verify)

	// Bench is populated by §A4 with the internet throughput benchmark so the
	// engine can lift it into the run Summary (nil until A4 runs; see BenchResult).
	Bench *BenchResult
}

// Step is one runbook block.
type Step interface {
	ID() string
	Title() string
	// Run returns a status, a short human detail line, and a hard error only for
	// lockout-capable failures that must abort the whole run.
	Run(ctx *Context) (Status, string, error)
}

// putFile returns a shell fragment that writes content to path with mode, using
// nested base64 so the outer base64 script delivery stays stdin-safe and the
// content needs no shell quoting (§A1 stdin caveat).
func putFile(path, content, mode string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf("echo '%s' | base64 -d > '%s'\nchmod %s '%s'\n", b64, path, mode, path)
}

// appendLineIfMissing returns a fragment that appends line to file only if an
// exact line match is absent (idempotent edit of a shared/non-owned file). The
// line is delivered via base64 so it needs no shell quoting.
func appendLineIfMissing(file, line string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(line))
	return fmt.Sprintf(
		"__L=$(echo '%s' | base64 -d); grep -qxF \"$__L\" '%s' 2>/dev/null || printf '%%s\\n' \"$__L\" >> '%s'\n",
		b64, file, file)
}

// freshLogin opens an INDEPENDENT new SSH session (the runbook's [LOCAL]
// second-session verify) using key auth as user, runs `true`, and closes it.
// Proves reachability without relying on the kept-open executor connection.
func freshLogin(ctx *Context, user string) error {
	c, err := sshx.Dial(ctx.Cfg.Host, ctx.Cfg.Port, user, "", ctx.KeyPEM)
	if err != nil {
		return err
	}
	defer c.Close()
	if r := c.Run("true"); !r.OK() {
		if r.Err != nil {
			return r.Err
		}
		return fmt.Errorf("remote `true` returned rc=%d", r.RC)
	}
	return nil
}
