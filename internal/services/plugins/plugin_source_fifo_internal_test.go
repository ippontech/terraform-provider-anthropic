// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package plugins

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A listed file swapped for a FIFO would block a plain open forever, before
// the check that refuses it, with the state lock held. The open does not
// block, so the read fails at once.
func TestPluginSource_refusesAFileSwappedForAFIFO(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)
	src, err := listPluginSource(root, "")
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	target := filepath.Join(root, "skills", "account-research", "SKILL.md")
	mustRemove(t, target)
	if err := syscall.Mkfifo(target, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := src.hash()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("hash succeeded on a FIFO")
		}
	case <-time.After(10 * time.Second):
		// Unblock the stuck open so the test binary can exit.
		if w, err := os.OpenFile(target, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("hash blocked on a FIFO")
	}
}
