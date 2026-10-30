// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build !unix

package plugins

import "errors"

// mkfifo reports that this platform has no named pipes; the test that needs
// one leaves its case out.
func mkfifo(string) error { return errors.New("named pipes are not available on this platform") }
