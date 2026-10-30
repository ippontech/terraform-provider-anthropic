// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import "testing"

func TestJSONDiff(t *testing.T) {
	for _, tc := range []struct {
		name, got, want, diff string
	}{
		{"equal despite order and spacing", `{"a":1,"b":[true,null]}`, `{ "b": [true, null], "a": 1 }`, ""},
		{"absent vs null", `{"m":{"k":null}}`, `{"m":{}}`, "$.m.k: got null, want absent"},
		{"null vs empty string", `{"k":""}`, `{"k":null}`, `$.k: got "", want null`},
		{"missing key", `{}`, `{"k":"v"}`, `$.k: got absent, want "v"`},
		{"number text", `{"n":1}`, `{"n":1.0}`, "$.n: got 1, want 1.0"},
		{"array element", `{"t":[{"n":"a"},{"n":"b"}]}`, `{"t":[{"n":"a"},{"n":"c"}]}`, `$.t[1].n: got "b", want "c"`},
		{"array length", `[1]`, `[1,2]`, "$: got 1 elements [1], want 2 [1,2]"},
		{"object vs scalar", `"x"`, `{}`, `$: got "x", want an object`},
		{"array vs object", `{}`, `[]`, "$: got {}, want an array"},
		{"several paths", `{"a":1,"b":2}`, `{"a":2,"c":3}`, "$.a: got 1, want 2\n$.b: got 2, want absent\n$.c: got absent, want 3"},
		{"got not JSON", `{`, `{}`, "got is not JSON (unexpected EOF): {"},
		{"want not JSON", `{}`, `{} {}`, "want is not JSON (trailing data after the first value): {} {}"},
	} {
		if got := JSONDiff(tc.got, tc.want); got != tc.diff {
			t.Errorf("%s: JSONDiff = %q, want %q", tc.name, got, tc.diff)
		}
	}
	if got := render(make(chan int)); got == "" {
		t.Error("render of a non-JSON value is empty")
	}
}
