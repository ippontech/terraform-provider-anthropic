// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package plugins

import (
	"os"
	"syscall"
)

// openFlags opens a plugin source file without blocking: a FIFO swapped in
// after the listing would otherwise block the open forever, before the type
// check that refuses it. O_NONBLOCK has no effect on a regular file.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK
