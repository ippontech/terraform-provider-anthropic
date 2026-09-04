// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

// Shared helpers for the vaults acceptance tests (vault + vault credential).
//
// The vaults API serves stale reads for up to ~1s after a write (see the
// read-after-write note in CLAUDE.md and #193): a Get issued right after the
// post-test destroy can still return the deleted — or not-yet-archived —
// object, which made CheckDestroy flaky in CI. The destroy/archive checks
// therefore poll until the API converges, reusing the ceiling and interval
// of awaitVaultUpdateVisible in the resource itself.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	destroyCheckTimeout  = 5 * time.Second
	destroyCheckInterval = 200 * time.Millisecond
)

// newAccTestClient builds the out-of-band client the destroy/archive checks
// use to observe the API directly, with the same key as the provider under test.
func newAccTestClient() anthropic.Client {
	return anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
}

// isNotFoundError reports whether err is an API 404.
func isNotFoundError(err error) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// awaitGone polls get until it returns 404 (destroyed) or the deadline passes.
// Any other error is returned as-is: it says nothing about visibility, and
// treating it as "destroyed" would let an auth failure pass the check.
func awaitGone(kind, id string, get func(ctx context.Context) error) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	for {
		err := get(context.Background())
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("checking %s %s after destroy: %w", kind, id, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s %s still exists %v after destroy", kind, id, destroyCheckTimeout)
		}
		time.Sleep(destroyCheckInterval)
	}
}

// awaitArchived polls get until it returns a non-zero archived_at, guarding
// against the same staleness window on the archive write. A read error fails
// immediately: the object existed before the archive, so a stale read shows
// the unarchived object, never a 404.
func awaitArchived(kind, id string, get func(ctx context.Context) (time.Time, error)) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	for {
		archivedAt, err := get(context.Background())
		if err != nil {
			return fmt.Errorf("%s %s not found after archive destroy: %w", kind, id, err)
		}
		if !archivedAt.IsZero() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s %s was not archived on destroy", kind, id)
		}
		time.Sleep(destroyCheckInterval)
	}
}

func awaitVaultGone(client anthropic.Client, id string) error {
	return awaitGone("vault", id, func(ctx context.Context) error {
		_, err := client.Beta.Vaults.Get(ctx, id, anthropic.BetaVaultGetParams{})
		return err
	})
}

func awaitVaultArchived(client anthropic.Client, id string) error {
	return awaitArchived("vault", id, func(ctx context.Context) (time.Time, error) {
		vault, err := client.Beta.Vaults.Get(ctx, id, anthropic.BetaVaultGetParams{})
		if err != nil {
			return time.Time{}, err
		}
		return vault.ArchivedAt, nil
	})
}

// awaitCredentialGone treats a 404 on either the credential or its parent
// vault as destroyed.
func awaitCredentialGone(client anthropic.Client, vaultID, id string) error {
	return awaitGone("vault credential", id, func(ctx context.Context) error {
		_, err := client.Beta.Vaults.Credentials.Get(ctx, id, anthropic.BetaVaultCredentialGetParams{
			VaultID: vaultID,
		})
		return err
	})
}

func awaitCredentialArchived(client anthropic.Client, vaultID, id string) error {
	return awaitArchived("credential", id, func(ctx context.Context) (time.Time, error) {
		cred, err := client.Beta.Vaults.Credentials.Get(ctx, id, anthropic.BetaVaultCredentialGetParams{
			VaultID: vaultID,
		})
		if err != nil {
			return time.Time{}, err
		}
		return cred.ArchivedAt, nil
	})
}

// hardDeleteVault permanently deletes an archived vault so it doesn't
// accumulate in the test workspace.
func hardDeleteVault(client anthropic.Client, id string) error {
	if _, err := client.Beta.Vaults.Delete(context.Background(), id, anthropic.BetaVaultDeleteParams{}); err != nil {
		return fmt.Errorf("cleanup: unable to delete archived vault %s: %w", id, err)
	}
	return nil
}
