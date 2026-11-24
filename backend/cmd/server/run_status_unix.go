//go:build !windows

package main

import (
	"os"
	"syscall"
)

// exitStatus is a finished program's status as shells report it: its exit
// code, or 128 + the signal that ended it
func exitStatus(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return state.ExitCode()
}
