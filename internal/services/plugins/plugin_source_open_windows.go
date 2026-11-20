// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build windows

package plugins

import "os"

// openFlags opens a plugin source file. Windows has no FIFO in the file
// system, so an open does not block on one.
const openFlags = os.O_RDONLY
