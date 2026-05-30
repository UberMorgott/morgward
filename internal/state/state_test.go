package state

import (
	"path/filepath"
	"testing"
)

func TestResetClearsCompleted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.state.json")
	c := Load(p)
	c.Mark("A1", "OK")
	c.Mark("A2", "OK")
	if !c.Done("A1") {
		t.Fatal("precondition: A1 should be done")
	}
	c.Reset()
	if c.Done("A1") || c.Done("A2") || len(c.Completed) != 0 {
		t.Fatalf("Reset did not clear Completed: %v", c.Completed)
	}
	// persisted empty
	if reloaded := Load(p); len(reloaded.Completed) != 0 {
		t.Fatalf("Reset not persisted: %v", reloaded.Completed)
	}
}
