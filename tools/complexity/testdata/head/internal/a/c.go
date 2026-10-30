// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package a

func init() {
	if Moved(1) == 2 {
		panic(2)
	}
}
