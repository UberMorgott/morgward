//go:build windows

package main

import (
	"time"

	"golang.org/x/term"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// resizePollInterval is how often the Windows feeder samples the terminal size.
// Windows has no SIGWINCH, so we poll; 250ms is responsive enough for a manual
// window drag while costing a single cheap GetSize per tick.
const resizePollInterval = 250 * time.Millisecond

// watchResize reports terminal-size changes of fd by polling on a ticker
// (Windows lacks SIGWINCH). It sends only when the size actually changes; the
// stop func ends the feeder goroutine. The channel is not closed by stop (the
// consumer's ctx/session lifetime ends Shell's resize goroutine).
func watchResize(fd int) (<-chan sshx.WinSize, func()) {
	ch := make(chan sshx.WinSize, 1)
	stop := make(chan struct{})

	lastCols, lastRows, _ := term.GetSize(fd)

	go func() {
		t := time.NewTicker(resizePollInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				cols, rows, err := term.GetSize(fd)
				if err != nil || (cols == lastCols && rows == lastRows) {
					continue
				}
				lastCols, lastRows = cols, rows
				// Keep only the latest size if the consumer is slow.
				select {
				case ch <- sshx.WinSize{Cols: cols, Rows: rows}:
				default:
					select {
					case <-ch:
					default:
					}
					select {
					case ch <- sshx.WinSize{Cols: cols, Rows: rows}:
					default:
					}
				}
			}
		}
	}()

	return ch, func() { close(stop) }
}
