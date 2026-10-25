//go:build !windows

package main

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
)

func openBrowser(address string) error {
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return fmt.Errorf("browser launch is unsupported on %s", runtime.GOOS)
	}
	process := exec.Command(command, address)
	if err := process.Start(); err != nil {
		return err
	}
	go func() {
		if err := process.Wait(); err != nil {
			log.Printf("Cannot open browser: %v; open %s manually", err, address)
		}
	}()
	return nil
}
