//go:build !windows

package main

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

func pingCamera(parent context.Context, host, bind string) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 900*time.Millisecond)
	defer cancel()
	args := []string{"-n", "-c", "1"}
	if bind != "" {
		flag := "-I"
		if runtime.GOOS == "darwin" {
			flag = "-S"
		}
		args = append(args, flag, bind)
	}
	args = append(args, host)
	err := exec.CommandContext(ctx, "ping", args...).Run()
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, err
}
