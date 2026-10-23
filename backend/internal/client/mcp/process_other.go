//go:build !windows

package mcp

import "os/exec"

func hideProcess(*exec.Cmd) {}
