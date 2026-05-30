package state

import "testing"

func TestInMemoryNoPersistence(t *testing.T) {
	c := Load("") // no path => fresh, never reads/writes disk
	if c.Done("A1") {
		t.Fatal("fresh checkpoint should have nothing done")
	}
	c.Mark("A1", "OK")
	if !c.Done("A1") {
		t.Fatal("Mark should update in-memory state")
	}
	c.Reset()
	if c.Done("A1") {
		t.Fatal("Reset should clear in-memory state")
	}
}
