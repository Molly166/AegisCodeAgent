//go:build !unix && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package liveeval

import "os/exec"

func configureProcess(command *exec.Cmd) {}
