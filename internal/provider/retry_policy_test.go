// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// retryFakeAPI serves just enough of the environments, files and skills
// endpoints for a create, a refresh and a destroy, and fails the requests a
// test scripts. Every failure carries retry-after-ms: 1, so a client that
// retries it does so at once and shows up in the request count.
type retryFakeAPI struct {
	mu       sync.Mutex
	failures map[string][]int // "METHOD /path" -> statuses for the next requests
	counts   map[string]int   // "METHOD /path" -> requests received
}

func newRetryFakeAPI(t *testing.T) *retryFakeAPI {
	t.Helper()
	f := &retryFakeAPI{failures: map[string][]int{}, counts: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "test-api-key")
	return f
}

func (f *retryFakeAPI) fail(route string, statuses ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[route] = append(f.failures[route], statuses...)
}

func (f *retryFakeAPI) count(route string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[route]
}

func (f *retryFakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	route := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.counts[route]++
	var status int
	if queue := f.failures[route]; len(queue) > 0 {
		status, f.failures[route] = queue[0], queue[1:]
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.Header().Set("retry-after-ms", "1")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"scripted %d"}}`, status)
		return
	}

	environment := map[string]any{
		"id": "env_1", "type": "environment", "name": "retry", "description": nil, "metadata": map[string]any{},
		"config": map[string]any{
			"type": "cloud", "networking": map[string]any{"type": "unrestricted"},
			"packages": map[string]any{"type": "packages", "apt": []any{}, "cargo": []any{}, "gem": []any{}, "go": []any{}, "npm": []any{}, "pip": []any{}},
		},
		"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z", "archived_at": nil,
	}
	switch route {
	case "POST /v1/environments", "GET /v1/environments/env_1", "POST /v1/environments/env_1/archive":
		_ = json.NewEncoder(w).Encode(environment)
	case "DELETE /v1/environments/env_1":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "env_1", "type": "environment_deleted"})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"no route"}}`))
	}
}

func retryProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"anthropic": providerserver.NewProtocol6WithError(New("test")()),
	}
}

const retryEnvironmentConfig = `
resource "anthropic_environment" "test" {
  name = "retry"
}
`

// A 5xx on a create is not replayed: the environment may have been created,
// and a second POST would leave a duplicate behind. The apply fails instead.
func TestRetryPolicy_createServerErrorIsNotRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)
	fake.fail("POST /v1/environments", http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      retryEnvironmentConfig,
			ExpectError: regexp.MustCompile(`Unable to create environment`),
		}},
	})

	if got := fake.count("POST /v1/environments"); got != 1 {
		t.Errorf("POST /v1/environments sent %d times, want 1", got)
	}
}

// A 429 says the request was not processed, so the create is replayed.
func TestRetryPolicy_createRateLimitedIsRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)
	fake.fail("POST /v1/environments", http.StatusTooManyRequests)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{{
			Config: retryEnvironmentConfig,
			Check:  resource.TestCheckResourceAttr("anthropic_environment.test", "id", "env_1"),
		}},
	})

	if got := fake.count("POST /v1/environments"); got != 2 {
		t.Errorf("POST /v1/environments sent %d times, want 2 (429, then success)", got)
	}
}

// A 529 says the API is overloaded, a capacity answer like a 429, so the
// create is replayed.
func TestRetryPolicy_createOverloadedIsRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)
	fake.fail("POST /v1/environments", 529)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{{
			Config: retryEnvironmentConfig,
			Check:  resource.TestCheckResourceAttr("anthropic_environment.test", "id", "env_1"),
		}},
	})

	if got := fake.count("POST /v1/environments"); got != 2 {
		t.Errorf("POST /v1/environments sent %d times, want 2 (529, then success)", got)
	}
}

// Archiving sets a state, so a replay cannot leave a duplicate: a transient
// 5xx on the archive of a destroy is replayed and the destroy succeeds.
func TestRetryPolicy_archiveServerErrorIsRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `
resource "anthropic_environment" "test" {
  name               = "retry"
  archive_on_destroy = true
}
`,
			Check: func(*terraform.State) error {
				fake.fail("POST /v1/environments/env_1/archive", http.StatusInternalServerError)
				return nil
			},
		}},
	})

	if got := fake.count("POST /v1/environments/env_1/archive"); got != 2 {
		t.Errorf("POST /v1/environments/env_1/archive sent %d times, want 2 (500, then success)", got)
	}
}

// Reads keep the SDK's retries: a transient 5xx on the refresh is replayed
// and the plan succeeds.
func TestRetryPolicy_readServerErrorIsRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)
	var before int

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{
			{Config: retryEnvironmentConfig},
			{
				PreConfig: func() {
					before = fake.count("GET /v1/environments/env_1")
					fake.fail("GET /v1/environments/env_1", http.StatusServiceUnavailable, http.StatusBadGateway)
				},
				Config:   retryEnvironmentConfig,
				PlanOnly: true,
			},
		},
	})

	// The plan-only step refreshes more than once; the first refresh used the
	// two failures and a third attempt.
	if got := fake.count("GET /v1/environments/env_1") - before; got < 3 {
		t.Errorf("GET /v1/environments/env_1 sent %d times in the plan step, want at least 3 (two 5xx retried)", got)
	}
}

// An upload is not replayed on a 5xx either: before this policy the SDK
// replayed the buffered multipart body, and one 5xx gave three POST
// /v1/files (three files if the first write had landed).
func TestRetryPolicy_fileUploadServerErrorIsNotRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)
	fake.fail("POST /v1/files", http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError)
	source := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "anthropic_file" "test" {
  source_path = %q
}
`, source),
			ExpectError: regexp.MustCompile(`Unable to upload file`),
		}},
	})

	if got := fake.count("POST /v1/files"); got != 1 {
		t.Errorf("POST /v1/files sent %d times, want 1", got)
	}
}

// A skill upload is not replayed on a 5xx: provretry.MultipartUpload used to
// retry 5xx three times on top of the SDK's three attempts, nine POSTs.
func TestRetryPolicy_skillUploadServerErrorIsNotRetried(t *testing.T) {
	fake := newRetryFakeAPI(t)
	fake.fail("POST /v1/skills", http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError,
		http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError,
		http.StatusInternalServerError, http.StatusInternalServerError, http.StatusInternalServerError)
	skillFile := filepath.Join(t.TempDir(), "retry-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("---\nname: retry-skill\ndescription: test\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: retryProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "anthropic_skill" "test" {
  files = [%q]
}
`, skillFile),
			ExpectError: regexp.MustCompile(`Unable to create skill`),
		}},
	})

	if got := fake.count("POST /v1/skills"); got != 1 {
		t.Errorf("POST /v1/skills sent %d times, want 1", got)
	}
}
