package main

import (
	"reflect"
	"testing"
)

// TestPartitionArgs proves flags work BEFORE or AFTER the step IDs, and that
// value-taking flags correctly absorb their following token so it is not mistaken
// for a step ID.
func TestPartitionArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantFlag []string
		wantPos  []string
	}{
		{
			name:     "flags after step ids",
			args:     []string{"A4", "A6.5", "--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantFlag: []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantPos:  []string{"A4", "A6.5"},
		},
		{
			name:     "flags before step ids",
			args:     []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes", "A4", "A6.5"},
			wantFlag: []string{"--host", "1.2.3.4", "--user", "root", "--assume-yes"},
			wantPos:  []string{"A4", "A6.5"},
		},
		{
			name:     "interleaved",
			args:     []string{"A4", "--host", "1.2.3.4", "A6.5", "--assume-yes"},
			wantFlag: []string{"--host", "1.2.3.4", "--assume-yes"},
			wantPos:  []string{"A4", "A6.5"},
		},
		{
			name:     "equals form keeps value attached",
			args:     []string{"--host=1.2.3.4", "A4"},
			wantFlag: []string{"--host=1.2.3.4"},
			wantPos:  []string{"A4"},
		},
		{
			name:     "value that looks like an id is not a step id",
			args:     []string{"--admin-user", "A4", "A5"},
			wantFlag: []string{"--admin-user", "A4"},
			wantPos:  []string{"A5"},
		},
		{
			name:     "no positionals",
			args:     []string{"--host", "1.2.3.4", "--assume-yes"},
			wantFlag: []string{"--host", "1.2.3.4", "--assume-yes"},
			wantPos:  nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlag, gotPos := partitionArgs(c.args)
			if !reflect.DeepEqual(gotFlag, c.wantFlag) {
				t.Errorf("flagArgs = %v, want %v", gotFlag, c.wantFlag)
			}
			if !reflect.DeepEqual(gotPos, c.wantPos) {
				t.Errorf("positional = %v, want %v", gotPos, c.wantPos)
			}
		})
	}
}
