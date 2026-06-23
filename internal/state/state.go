// Package state holds an in-memory WORKFLOW checkpoint (which step, what is
// pending) for a single run. Nothing is persisted to disk: every run starts
// fresh, so there is no cross-invocation skip and no morgward-<host>.state.json
// file. The on-box configs remain the durable data checkpoints.
package state

// Checkpoint is the in-memory run state. Nothing is persisted (see Save), so no
// field carries a json tag — they are working scratch for the current run only.
type Checkpoint struct {
	Host       string            // connect target (set by the engine for logging context)
	AdminUser  string            // non-root admin (set by PRE)
	Completed  map[string]string // stepID -> status (OK/SKIP)
	BootID     string            // last observed boot_id (set by A8 across the reboot)
	Greenfield bool              // fresh-box flag snapshotted from detect.Facts
}

// Load returns a fresh in-memory checkpoint. The path argument is ignored; no
// disk is read.
func Load(_ string) *Checkpoint {
	return &Checkpoint{Completed: map[string]string{}}
}

// Done reports whether a step already completed successfully (OK or SKIP).
func (c *Checkpoint) Done(stepID string) bool {
	s, ok := c.Completed[stepID]
	return ok && (s == "OK" || s == "SKIP")
}

// Mark records a step result in memory.
func (c *Checkpoint) Mark(stepID, status string) {
	c.Completed[stepID] = status
}

// Reset clears the completed-step record in memory.
func (c *Checkpoint) Reset() {
	c.Completed = map[string]string{}
}

// Save is a no-op: the checkpoint lives only in memory. Kept so callers that
// expect to persist progress continue to compile.
func (c *Checkpoint) Save() {}
