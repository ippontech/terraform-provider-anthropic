// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package a is a fixture of the complexity ratchet: a function scores the
// number of if statements it holds. The tests cap it at 3.
package a

// T has a method above the cap.
type T struct{}

func Untouched(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	if x == 5 {
		return 5
	}
	return 0
}

func Touched(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	if x == 5 {
		return 5
	}
	return 0
}

func TouchedLower(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	if x == 5 {
		return 5
	}
	return 0
}

func Rose(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	return 0
}

func Crossed(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	return 0
}

func Lowered(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	if x == 5 {
		return 5
	}
	return 0
}

func Removed(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	if x == 5 {
		return 5
	}
	return 0
}

func Moved(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	if x == 5 {
		return 5
	}
	return 0
}

func (t *T) Method(x int) int {
	if x == 1 {
		return 1
	}
	if x == 2 {
		return 2
	}
	if x == 3 {
		return 3
	}
	if x == 4 {
		return 4
	}
	return 0
}

func init() {
	if Untouched(1) == 2 {
		panic(1)
	}
}
