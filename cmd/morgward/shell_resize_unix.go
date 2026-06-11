//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/UberMorgott/morgward/internal/sshx"
)

// watchResize reports terminal-size changes of fd via SIGWINCH (Unix). On each
// signal it re-reads the size and sends it on the returned channel; the stop func
// detaches the signal handler and ends the feeder goroutine. The channel is NOT
// closed by stop (the consumer's ctx/session lifetime ends Shell's resize
// goroutine), it simply stops receiving.
func watchResize(fd int) (<-chan sshx.WinSize, func()) {
	ch := make(chan sshx.WinSize, 1)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)

	go func() {
		for range sig {
			cols, rows, err := term.GetSize(fd)
			if err != nil {
				continue
			}
			// Drop the oldest pending update if the consumer is slow — only the
			// latest size matters.
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
	}()

	return ch, func() { signal.Stop(sig); close(sig) }
}
