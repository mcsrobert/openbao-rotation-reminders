package app

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	topic = "private-topic-4f2a9c"
	jwt   = "test-jwt"
	role  = "openbao-rotation-reminders"
)

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func custom(values string) string {
	return fmt.Sprintf(`{"current_version":1,"versions":{"1":{"created_time":"2026-03-01T12:00:00Z","deletion_time":"","destroyed":false}},"custom_metadata":%s}`, values)
}

// fakeBao serves Kubernetes login, token revocation and KV v2 metadata for
// the secret mount over TLS, and fails the test on any other request.
type fakeBao struct {
	t            *testing.T
	server       *httptest.Server
	caFile       string
	secrets      map[string]string
	tokenType    string
	loginStatus  int
	listStatus   int
	revokeStatus int

	mu       sync.Mutex
	requests []string
}

func newFakeBao(t *testing.T, secrets map[string]string) *fakeBao {
	t.Helper()
	bao := &fakeBao{t: t, secrets: secrets, tokenType: "service"}
	bao.server = httptest.NewTLSServer(http.HandlerFunc(bao.serve))
	t.Cleanup(bao.server.Close)
	bao.caFile = filepath.Join(t.TempDir(), "ca.crt")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bao.server.Certificate().Raw})
	if err := os.WriteFile(bao.caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	return bao
}

func (b *fakeBao) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.requests = append(b.requests, r.Method+" "+r.URL.Path)
	b.mu.Unlock()

	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/v1/auth/kubernetes/login":
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["jwt"] != jwt || body["role"] != role {
			b.t.Errorf("login body = %v, %v", body, err)
		}
		if b.loginStatus != 0 {
			w.WriteHeader(b.loginStatus)
			return
		}
		_, _ = fmt.Fprintf(w, `{"auth":{"client_token":"s.token","token_type":%q}}`, b.tokenType)
		return
	case r.Header.Get("X-Vault-Token") != "s.token":
		b.t.Errorf("%s %s without the token", r.Method, path)
		w.WriteHeader(http.StatusForbidden)
		return
	case r.Method == http.MethodPost && path == "/v1/auth/token/revoke-self":
		w.WriteHeader(cmpOr(b.revokeStatus, http.StatusNoContent))
		return
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/secret/metadata/"):
		key := strings.TrimPrefix(path, "/v1/secret/metadata/")
		if r.URL.Query().Get("list") == "true" {
			b.list(w, key)
			return
		}
		metadata, ok := b.secrets[key]
		if !ok {
			notFound(w)
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":%s}`, metadata)
		return
	}
	b.t.Errorf("unexpected request %s %s", r.Method, path)
	w.WriteHeader(http.StatusForbidden)
}

func (b *fakeBao) list(w http.ResponseWriter, directory string) {
	if b.listStatus != 0 {
		w.WriteHeader(b.listStatus)
		return
	}
	var entries []string
	for key := range b.secrets {
		rest, ok := strings.CutPrefix(key, directory)
		if !ok {
			continue
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i+1]
		}
		if !slices.Contains(entries, rest) {
			entries = append(entries, rest)
		}
	}
	if len(entries) == 0 {
		notFound(w)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keys": entries}})
}

// notFound responds like OpenBao does for a missing key or an empty
// directory.
func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"errors":[]}`)
}

func (b *fakeBao) count(request string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, r := range b.requests {
		if r == request {
			count++
		}
	}
	return count
}

type notification struct {
	path   string
	header http.Header
	body   string
}

type fakeNtfy struct {
	server *httptest.Server
	status int

	mu            sync.Mutex
	notifications []notification
}

func newFakeNtfy(t *testing.T) *fakeNtfy {
	t.Helper()
	n := &fakeNtfy{}
	n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n.mu.Lock()
		n.notifications = append(n.notifications, notification{r.URL.Path, r.Header.Clone(), string(body)})
		n.mu.Unlock()
		w.WriteHeader(cmpOr(n.status, http.StatusOK))
	}))
	t.Cleanup(n.server.Close)
	return n
}

func (n *fakeNtfy) received() []notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.notifications)
}

func cmpOr(value, fallback int) int {
	if value != 0 {
		return value
	}
	return fallback
}

type run struct {
	status int
	stdout string
	stderr string
}

func environment(t *testing.T, bao *fakeBao, ntfy *fakeNtfy) map[string]string {
	t.Helper()
	jwtFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwtFile, []byte(jwt+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"OPENBAO_ADDR":     bao.server.URL,
		"OPENBAO_MOUNT":    "secret",
		"OPENBAO_K8S_ROLE": role,
		"OPENBAO_CA_FILE":  bao.caFile,
		"OPENBAO_JWT_FILE": jwtFile,
		"OPENBAO_UI_URL":   "https://bao.example.com",
		"NTFY_URL":         ntfy.server.URL,
		"NTFY_TOPIC":       topic,
	}
}

func execute(t *testing.T, env map[string]string) run {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := Run(t.Context(), func(name string) string { return env[name] }, &stdout, &stderr, now)
	result := run{status, stdout.String(), stderr.String()}
	if strings.Contains(result.stdout+result.stderr, topic) {
		t.Errorf("the output contains the topic:\n%s%s", result.stdout, result.stderr)
	}
	return result
}

func TestRunPublishesADigest(t *testing.T) {
	bao := newFakeBao(t, map[string]string{
		"app/expired":     custom(`{"expires_at":"2026-05-01"}`),
		"app/due":         custom(`{"rotation_interval":"90d","rotation_note":"Regenerate in\n the console"}`),
		"app/soon":        custom(`{"expires_at":"2026-06-07"}`),
		"app/typo":        custom(`{"rotation_intervall":"90d"}`),
		"app/fine":        custom(`{"rotation_interval":"180d"}`),
		"other/untracked": custom(`{"owner":"me"}`),
		"other/deleted":   `{"current_version":1,"versions":{"1":{"created_time":"2026-03-01T12:00:00Z","deletion_time":"2026-04-01T00:00:00Z"}},"custom_metadata":{"expires_at":"2020-01-01"}}`,
	})
	ntfy := newFakeNtfy(t)

	got := execute(t, environment(t, bao, ntfy))

	wantBody := `EXPIRED:
- app/expired: expired 2026-05-01 (31d ago)

DUE FOR ROTATION:
- app/due: due 2026-05-30 (2d overdue, interval 90d)
  Note: Regenerate in the console

EXPIRING SOON:
- app/soon: expires 2026-06-07 (in 5d)

INVALID METADATA:
- app/typo: metadata error: unknown metadata key "rotation_intervall"`
	wantTitle := "OpenBao: 1 expired, 1 due, 1 expiring soon, 1 invalid"
	wantStdout := "scanned=7 tracked=5 findings=4\n--- " + wantTitle + " (priority=urgent) ---\n" + wantBody + "\n"
	if got.status != 0 || got.stdout != wantStdout || got.stderr != "" {
		t.Fatalf("status %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", got.status, got.stdout, wantStdout, got.stderr)
	}

	received := ntfy.received()
	if len(received) != 1 {
		t.Fatalf("received %d notifications", len(received))
	}
	message := received[0]
	if message.path != "/"+topic || message.body != wantBody {
		t.Errorf("notification %s:\n%s", message.path, message.body)
	}
	for header, want := range map[string]string{
		"Title":    wantTitle,
		"Priority": "urgent",
		"Tags":     "rotating_light,key",
		"Click":    "https://bao.example.com",
	} {
		if got := message.header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if bao.count("POST /v1/auth/token/revoke-self") != 1 {
		t.Error("the token was not revoked once")
	}
}

func TestRunWithoutFindings(t *testing.T) {
	bao := newFakeBao(t, map[string]string{
		"app/fine":  custom(`{"rotation_interval":"180d"}`),
		"untracked": custom(`null`),
	})
	ntfy := newFakeNtfy(t)

	got := execute(t, environment(t, bao, ntfy))
	if got.status != 0 || got.stdout != "scanned=2 tracked=1 findings=0\n" || got.stderr != "" {
		t.Fatalf("got %+v", got)
	}
	if len(ntfy.received()) != 0 {
		t.Error("published without findings")
	}
	if bao.count("POST /v1/auth/token/revoke-self") != 1 {
		t.Error("the token was not revoked")
	}
}

func TestRunOfAnEmptyMount(t *testing.T) {
	bao := newFakeBao(t, nil)
	got := execute(t, environment(t, bao, newFakeNtfy(t)))
	if got.status != 0 || got.stdout != "scanned=0 tracked=0 findings=0\n" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunTruncatesTheNotificationButLogsEverything(t *testing.T) {
	secrets := map[string]string{}
	for i := range 150 {
		secrets[fmt.Sprintf("app/a-rather-long-secret-name-%03d", i)] = custom(`{"expires_at":"2026-05-01"}`)
	}
	bao := newFakeBao(t, secrets)
	ntfy := newFakeNtfy(t)

	got := execute(t, environment(t, bao, ntfy))
	if got.status != 0 {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.stdout, "- app/a-rather-long-secret-name-149: expired") {
		t.Error("the log does not list every finding")
	}
	body := ntfy.received()[0].body
	if len(body) > 4096 || !strings.HasSuffix(body, "\n... (truncated; check pod logs for full list)") {
		t.Errorf("notification body is %d bytes:\n%s", len(body), body)
	}
	if !strings.Contains(got.stdout, "OpenBao: 150 secrets expired (priority=urgent)") {
		t.Errorf("stdout:\n%s", got.stdout)
	}
}

func TestRunConfigurationErrors(t *testing.T) {
	bao := newFakeBao(t, nil)
	ntfy := newFakeNtfy(t)
	tests := []struct {
		name   string
		change map[string]string
		want   string
	}{
		{"missing address", map[string]string{"OPENBAO_ADDR": ""}, "configuration error: OPENBAO_ADDR: required\n"},
		{"invalid topic", map[string]string{"NTFY_TOPIC": topic + "?x=1"}, "configuration error: NTFY_TOPIC must be 1 to 64 letters, digits, hyphens or underscores\n"},
		{"missing CA file", map[string]string{"OPENBAO_CA_FILE": "/nonexistent/ca.crt"}, "configuration error: OPENBAO_CA_FILE: read CA file: open /nonexistent/ca.crt: no such file or directory\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := environment(t, bao, ntfy)
			for key, value := range test.change {
				env[key] = value
			}
			got := execute(t, env)
			if got.status != 1 || got.stdout != "" || got.stderr != test.want {
				t.Fatalf("got %+v, want stderr %q", got, test.want)
			}
		})
	}
	if bao.count("POST /v1/auth/kubernetes/login") != 0 {
		t.Error("logged in despite a configuration error")
	}
}

func TestRunOpenBaoErrors(t *testing.T) {
	emptyJWT := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyJWT, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		setup   func(*fakeBao, map[string]string)
		want    string
		revoked int
	}{
		{
			name:  "missing JWT",
			setup: func(_ *fakeBao, env map[string]string) { env["OPENBAO_JWT_FILE"] = "/nonexistent/token" },
			want:  "openbao error: failed to read JWT file: open /nonexistent/token: no such file or directory\n",
		},
		{
			name:  "empty JWT",
			setup: func(_ *fakeBao, env map[string]string) { env["OPENBAO_JWT_FILE"] = emptyJWT },
			want:  "openbao error: JWT file " + emptyJWT + " is empty\n",
		},
		{
			name:  "login denied",
			setup: func(b *fakeBao, _ map[string]string) { b.loginStatus = http.StatusForbidden },
			want:  "openbao error: HTTP 403 calling POST /v1/auth/kubernetes/login\n",
		},
		{
			name:    "listing fails",
			setup:   func(b *fakeBao, _ map[string]string) { b.listStatus = http.StatusInternalServerError },
			want:    "openbao error: HTTP 500 calling GET /v1/secret/metadata/\n",
			revoked: 1,
		},
		{
			name:    "missing mount",
			setup:   func(b *fakeBao, _ map[string]string) { b.listStatus = http.StatusNotFound },
			want:    "openbao error: HTTP 404 calling GET /v1/secret/metadata/: no such route, check OPENBAO_ADDR and OPENBAO_MOUNT\n",
			revoked: 1,
		},
		{
			name: "untrusted server",
			setup: func(b *fakeBao, env map[string]string) {
				env["OPENBAO_CA_FILE"] = ""
			},
			want: "openbao error: request failed calling POST /v1/auth/kubernetes/login: tls: failed to verify certificate: x509: certificate signed by unknown authority\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bao := newFakeBao(t, map[string]string{"app/key": custom(`{"expires_at":"2020-01-01"}`)})
			ntfy := newFakeNtfy(t)
			env := environment(t, bao, ntfy)
			test.setup(bao, env)
			got := execute(t, env)
			if got.status != 1 || got.stdout != "" || got.stderr != test.want {
				t.Fatalf("got %+v, want stderr %q", got, test.want)
			}
			if revoked := bao.count("POST /v1/auth/token/revoke-self"); revoked != test.revoked {
				t.Errorf("revoked %d times, want %d", revoked, test.revoked)
			}
			if len(ntfy.received()) != 0 {
				t.Error("published after an OpenBao error")
			}
		})
	}
}

func TestRunRevocationFailureIsOnlyAWarning(t *testing.T) {
	bao := newFakeBao(t, map[string]string{"app/fine": custom(`{"rotation_interval":"180d"}`)})
	bao.revokeStatus = http.StatusForbidden
	got := execute(t, environment(t, bao, newFakeNtfy(t)))
	want := "warning: token revocation failed: HTTP 403 calling POST /v1/auth/token/revoke-self\n"
	if got.status != 0 || got.stdout != "scanned=1 tracked=1 findings=0\n" || got.stderr != want {
		t.Fatalf("got %+v", got)
	}
}

func TestRunRevocationFailureDoesNotMaskAnotherError(t *testing.T) {
	bao := newFakeBao(t, map[string]string{"app/key": custom(`{"expires_at":"2020-01-01"}`)})
	bao.revokeStatus = http.StatusForbidden
	ntfy := newFakeNtfy(t)
	ntfy.status = http.StatusInternalServerError
	got := execute(t, environment(t, bao, ntfy))
	want := "warning: token revocation failed: HTTP 403 calling POST /v1/auth/token/revoke-self\n" +
		"notification error: ntfy publish failed with HTTP 500\n"
	if got.status != 1 || got.stderr != want {
		t.Fatalf("got %+v", got)
	}
}

func TestRunDoesNotRevokeBatchTokens(t *testing.T) {
	bao := newFakeBao(t, map[string]string{"app/fine": custom(`{"rotation_interval":"180d"}`)})
	bao.tokenType = "batch"
	got := execute(t, environment(t, bao, newFakeNtfy(t)))
	if got.status != 0 || got.stderr != "" {
		t.Fatalf("got %+v", got)
	}
	if bao.count("POST /v1/auth/token/revoke-self") != 0 {
		t.Error("revoked a batch token")
	}
}

func TestRunNotificationErrors(t *testing.T) {
	bao := newFakeBao(t, map[string]string{"app/key": custom(`{"expires_at":"2020-01-01"}`)})
	ntfy := newFakeNtfy(t)
	ntfy.status = http.StatusTooManyRequests
	got := execute(t, environment(t, bao, ntfy))
	if got.status != 1 || got.stderr != "notification error: ntfy publish failed with HTTP 429\n" {
		t.Fatalf("got %+v", got)
	}
	if !strings.HasPrefix(got.stdout, "scanned=1 tracked=1 findings=1\n--- OpenBao: app/key expired (priority=urgent) ---\n") {
		t.Errorf("stdout:\n%s", got.stdout)
	}

	retryFor = 0
	t.Cleanup(func() { retryFor = 30 * time.Second })
	unreachable := newFakeNtfy(t)
	unreachable.server.Close()
	env := environment(t, bao, unreachable)
	got = execute(t, env)
	host := strings.TrimPrefix(unreachable.server.URL, "http://")
	if got.status != 1 || !strings.HasPrefix(got.stderr, "notification error: ntfy publish failed: dial tcp "+host) {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(got.stderr, (&url.URL{Scheme: "http", Host: host, Path: "/" + topic}).String()) {
		t.Error("the error contains the request URL")
	}
}

func TestRetryConnect(t *testing.T) {
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay = time.Second })
	refused := fmt.Errorf("request failed: %w", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})
	reset := fmt.Errorf("request failed: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET})

	calls := 0
	err := retryConnect(t.Context(), func() error {
		if calls++; calls < 3 {
			return refused
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("refused twice: err = %v after %d calls", err, calls)
	}

	calls = 0
	err = retryConnect(t.Context(), func() error { calls++; return reset })
	if !errors.Is(err, reset) || calls != 1 {
		t.Errorf("a request that reached the server: err = %v after %d calls", err, calls)
	}

	retryFor = 50 * time.Millisecond
	t.Cleanup(func() { retryFor = 30 * time.Second })
	err = retryConnect(t.Context(), func() error { return refused })
	if !errors.Is(err, refused) {
		t.Errorf("always refused: err = %v", err)
	}
}
