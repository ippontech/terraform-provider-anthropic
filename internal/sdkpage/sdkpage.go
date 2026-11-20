// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package sdkpage walks the SDK's cursor-paginated lists with a bound, where
// the SDK's auto-pager would follow the API's cursor forever.
package sdkpage

import (
	"fmt"

	"github.com/anthropics/anthropic-sdk-go/packages/pagination"
)

// MaxPages bounds a list walk: at the page sizes the API serves, far more items
// than a workspace or an organization can hold.
const MaxPages = 10000

// Cursor visits every item of a `next_page` list, starting from the first page
// and its error as the SDK's List method returns them, and stops on the first
// error, from the API or from visit.
//
// Only an empty `next_page` ends the walk: a page can be empty while the
// cursor is still set, and the SDK's auto-pager follows the cursor past it
// too (since v1.77.0). Unlike the auto-pager, a cursor the API already
// returned, or a walk past MaxPages, is an error: an API that keeps returning
// the same cursor, or cycles through a few, empty pages included, would
// otherwise hang plan and refresh until a context deadline.
func Cursor[T any](first *pagination.PageCursor[T], err error, visit func(T) error) error {
	return cursorUpTo(first, err, MaxPages, visit)
}

// cursorUpTo is Cursor with its page bound as a parameter.
func cursorUpTo[T any](page *pagination.PageCursor[T], err error, maxPages int, visit func(T) error) error {
	seen := map[string]bool{}
	for pages := 1; ; pages++ {
		if err != nil {
			return err
		}
		if page == nil {
			return nil
		}
		for _, item := range page.Data {
			if err := visit(item); err != nil {
				return err
			}
		}
		next := page.NextPage
		switch {
		case next == "":
			return nil
		case seen[next]:
			return fmt.Errorf("the API returned the next_page cursor %q again, so the list cannot be read to its end", next)
		case pages >= maxPages:
			return fmt.Errorf("the list still had a next_page cursor after %d pages; stopping", maxPages)
		}
		seen[next] = true
		page, err = page.GetNextPage()
	}
}
