package main

import (
	"os/exec"
	"strings"
	"time"
)

func shouldRetry(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	retryablePatterns := []string{
		"cannot lock ref",
		"unable to update local ref",
		"refs/remotes/origin/",
		"is at .* but expected",
		"fatal: unable to access",
		"fatal: the remote end hung up unexpectedly",
		"fatal: early EOF",
		"fatal: index-pack failed",
		"fatal: pack-objects failed",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}
	return false
}

func git(cmd *exec.Cmd) ([]byte, error) {
	maxAttempts := 3

	var output []byte
	var err error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		output, err = cmd.Output()
		if err == nil || !shouldRetry(err) {
			break
		}

		if attempt < maxAttempts {
			time.Sleep(2 * time.Second)
		}
	}

	return output, err
}
