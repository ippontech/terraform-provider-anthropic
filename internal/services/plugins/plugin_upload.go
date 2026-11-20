// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"time"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

const (
	// pluginUploadBaseTimeout and pluginUploadMinBytesPerSecond size the
	// timeout of an upload: the time to send its body at 1 Mbit/s, plus two
	// minutes for the API to store the files and register the version before
	// it answers. A 200 MB upload gets about half an hour.
	pluginUploadBaseTimeout       = 2 * time.Minute
	pluginUploadMinBytesPerSecond = 128 << 10
)

// withUploadTimeout returns a client whose per-request timeout leaves an
// upload of size bytes time to finish. The admin client's own timeout (60
// seconds) covers the whole exchange, body included, which a large upload on
// a slow link can outlast while the API stores it anyway; an upload is never
// resent, so it would be stored but missing from state. The copy shares the
// client's transport; the shared client is not modified, and a client with
// no timeout, or a longer one, is returned as is.
func withUploadTimeout(client *admin.Client, size int) *admin.Client {
	timeout := pluginUploadBaseTimeout + time.Duration(size/pluginUploadMinBytesPerSecond+1)*time.Second
	if client.HTTPClient == nil || client.HTTPClient.Timeout == 0 || client.HTTPClient.Timeout >= timeout {
		return client
	}
	httpClient := *client.HTTPClient
	httpClient.Timeout = timeout
	upload := *client
	upload.HTTPClient = &httpClient
	return &upload
}
