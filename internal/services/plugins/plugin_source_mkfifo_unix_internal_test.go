// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build unix

package plugins

import "syscall"

// mkfifo makes a named pipe, a special file the plugin source must refuse.
func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }
