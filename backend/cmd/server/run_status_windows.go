//go:build windows

package main

import "os"

// exitStatus is a finished program's exit code
func exitStatus(state *os.ProcessState) int {
	return state.ExitCode()
}
