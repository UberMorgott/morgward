package sshx

import (
	"bufio"
	"strings"
	"testing"
)

// apt/dpkg redraw progress with bare CR ("Waiting for cache lock: … [Ns]");
// the tee scanner must surface each redraw as a line instead of sitting on the
// whole sequence until the command exits (which read as a dead run in the TUI).
func TestScanCRLFLinesSplitsOnBareCR(t *testing.T) {
	in := "Waiting for cache lock [1s]\rWaiting for cache lock [2s]\rdone\r\nnext\nlast"
	sc := bufio.NewScanner(strings.NewReader(in))
	sc.Split(scanCRLFLines)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	want := []string{"Waiting for cache lock [1s]", "Waiting for cache lock [2s]", "done", "next", "last"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}
