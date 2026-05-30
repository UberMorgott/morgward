// Package state holds an in-memory WORKFLOW checkpoint (which step, what is
// pending) for a single run. Nothing is persisted to disk: every run starts
// fresh, so there is no cross-invocation skip and no morgward-<host>.state.json
// file. The on-box configs remain the durable data checkpoints.
package state

// Checkpoint is the in-memory run state.
type Checkpoint struct {
	Host       string            `json:"host"`
	AdminUser  string            `json:"admin_user"`
	Mode       string            `json:"mode"`
	KeyPath    string            `json:"key_path"`
	Completed  map[string]string `json:"completed"` // stepID -> status (OK/SKIP)
	BootID     string            `json:"boot_id"`
	Greenfield bool              `json:"greenfield"`
	UpdatedAt  string            `json:"updated_at"`
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
