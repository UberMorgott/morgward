// Package state persists a local checkpoint so a re-run knows what already
// completed. The on-box configs are the data checkpoints; this file is the
// orchestrator's WORKFLOW checkpoint (which step, what is pending) per §0.5.
package state

import (
	"encoding/json"
	"os"
	"time"
)

// Checkpoint is the serialized run state.
type Checkpoint struct {
	Host       string            `json:"host"`
	AdminUser  string            `json:"admin_user"`
	Mode       string            `json:"mode"`
	KeyPath    string            `json:"key_path"`
	Completed  map[string]string `json:"completed"` // stepID -> status (OK/SKIP)
	BootID     string            `json:"boot_id"`
	Greenfield bool              `json:"greenfield"`
	UpdatedAt  string            `json:"updated_at"`

	path string
}

// Load reads a checkpoint from path, or returns a fresh one if absent.
func Load(path string) *Checkpoint {
	c := &Checkpoint{Completed: map[string]string{}, path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, c)
	if c.Completed == nil {
		c.Completed = map[string]string{}
	}
	c.path = path
	return c
}

// Done reports whether a step already completed successfully (OK or SKIP).
func (c *Checkpoint) Done(stepID string) bool {
	s, ok := c.Completed[stepID]
	return ok && (s == "OK" || s == "SKIP")
}

// Mark records a step result and persists immediately.
func (c *Checkpoint) Mark(stepID, status string) {
	c.Completed[stepID] = status
	c.Save()
}

// Save writes the checkpoint to disk.
func (c *Checkpoint) Save() {
	if c.path == "" {
		return
	}
	c.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(c.path, data, 0o600)
}
