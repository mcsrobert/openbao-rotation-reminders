//go:build integration

// Package integration runs the reminders against a real OpenBao dev server
// with TLS and Kubernetes auth, and a local ntfy receiver. It needs the bao
// binary on PATH.
package integration

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/app"
)

const (
	rootToken = "integration-root"
	topic     = "integration-topic-7d1c"

	serviceAccountNamespace = "rotation"
	serviceAccountName      = "openbao-rotation-reminders"
	serviceAccountUID       = "4b1e8c1a-0000-4000-8000-000000000001"
	audience                = "openbao"
)

type bao struct {
	t       *testing.T
	address string
	caFile  string
	client  *http.Client
}

func startBao(t *testing.T) *bao {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	certDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "bao", "server", "-dev", "-dev-tls",
		"-dev-tls-cert-dir="+certDir,
		"-dev-root-token-id="+rootToken,
		"-dev-listen-address="+address,
		"-dev-no-store-token",
	)
	logFile, err := os.Create(filepath.Join(t.TempDir(), "bao.log"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start bao: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
		_ = logFile.Close()
	})

	server := &bao{t: t, address: "https://" + address, caFile: filepath.Join(certDir, "vault-ca.pem")}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if pem, err := os.ReadFile(server.caFile); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				server.client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
				if status, _ := server.request(http.MethodGet, "sys/health", rootToken, nil); status == http.StatusOK {
					return server
				}
			}
		}
		if time.Now().After(deadline) {
			logs, _ := os.ReadFile(logFile.Name())
			t.Fatalf("bao did not become healthy:\n%s", logs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (b *bao) request(method, path, token string, body any) (int, []byte) {
	b.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, b.address+"/v1/"+path, reader)
	if err != nil {
		b.t.Fatal(err)
	}
	request.Header.Set("X-Vault-Token", token)
	response, err := b.client.Do(request)
	if err != nil {
		return 0, nil
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return response.StatusCode, content
}

func (b *bao) mustRoot(method, path string, body any) []byte {
	b.t.Helper()
	status, content := b.request(method, path, rootToken, body)
	if status < 200 || status > 299 {
		b.t.Fatalf("%s %s = %d: %s", method, path, status, content)
	}
	return content
}

func (b *bao) writeSecret(key string, custom map[string]string) {
	b.t.Helper()
	b.mustRoot(http.MethodPost, "secret/data/"+escape(key), map[string]any{"data": map[string]string{"value": "do-not-read"}})
	if custom != nil {
		b.mustRoot(http.MethodPost, "secret/metadata/"+escape(key), map[string]any{"custom_metadata": custom})
	}
}

// writePolicy writes examples/policy.hcl as the reminders policy, with its
// mount set to secret.
func (b *bao) writePolicy() {
	b.t.Helper()
	policy, err := os.ReadFile(filepath.Join("..", "..", "examples", "policy.hcl"))
	if err != nil {
		b.t.Fatal(err)
	}
	b.mustRoot(http.MethodPost, "sys/policies/acl/reminders", map[string]string{"policy": string(bytes.ReplaceAll(policy, []byte("MOUNT"), []byte("secret")))})
}

func (b *bao) tokenCount() int {
	b.t.Helper()
	var response struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b.mustRoot("LIST", "auth/token/accessors", nil), &response); err != nil {
		b.t.Fatal(err)
	}
	return len(response.Data.Keys)
}

func escape(key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// serviceAccountJWT returns a projected service account token for the
// openbao audience. OpenBao only parses it and relies on the token review.
func serviceAccountJWT(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(encoded)
	}
	now := time.Now()
	signed := encode(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + encode(map[string]any{
		"iss": "https://kubernetes.default.svc",
		"sub": "system:serviceaccount:" + serviceAccountNamespace + ":" + serviceAccountName,
		"aud": []string{audience},
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
		"kubernetes.io": map[string]any{
			"namespace":      serviceAccountNamespace,
			"serviceaccount": map[string]string{"name": serviceAccountName, "uid": serviceAccountUID},
		},
	})
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// tokenReviewServer stands in for the Kubernetes API. It accepts every token
// as the reminders' service account when the review asks for the openbao
// audience.
func tokenReviewServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/authentication.k8s.io/v1/tokenreviews" {
			t.Errorf("unexpected Kubernetes API request %s", r.URL.Path)
		}
		var review struct {
			Spec struct {
				Audiences []string `json:"audiences"`
			} `json:"spec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil || len(review.Spec.Audiences) != 1 || review.Spec.Audiences[0] != audience {
			t.Errorf("token review audiences = %v, %v", review.Spec.Audiences, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": "authentication.k8s.io/v1",
			"kind":       "TokenReview",
			"status": map[string]any{
				"authenticated": true,
				"audiences":     []string{audience},
				"user": map[string]string{
					"username": "system:serviceaccount:" + serviceAccountNamespace + ":" + serviceAccountName,
					"uid":      serviceAccountUID,
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

type notification struct {
	path   string
	header http.Header
	body   string
}

type ntfyReceiver struct {
	server        *httptest.Server
	mu            sync.Mutex
	notifications []notification
}

func startNtfy(t *testing.T) *ntfyReceiver {
	t.Helper()
	receiver := &ntfyReceiver{}
	receiver.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receiver.mu.Lock()
		receiver.notifications = append(receiver.notifications, notification{r.URL.Path, r.Header.Clone(), string(body)})
		receiver.mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"test","event":"message"}`)
	}))
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (n *ntfyReceiver) take() []notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	notifications := n.notifications
	n.notifications = nil
	return notifications
}

func TestAgainstRealOpenBao(t *testing.T) {
	server := startBao(t)
	server.writePolicy()
	server.mustRoot(http.MethodPost, "sys/auth/kubernetes", map[string]string{"type": "kubernetes"})
	kubernetes := tokenReviewServer(t)
	server.mustRoot(http.MethodPost, "auth/kubernetes/config", map[string]any{
		"kubernetes_host":      kubernetes.URL,
		"kubernetes_ca_cert":   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: kubernetes.Certificate().Raw})),
		"disable_local_ca_jwt": true,
	})
	for role, tokenType := range map[string]string{"openbao-rotation-reminders": "service", "batch": "batch"} {
		server.mustRoot(http.MethodPost, "auth/kubernetes/role/"+role, map[string]any{
			"bound_service_account_names":      []string{serviceAccountName},
			"bound_service_account_namespaces": []string{serviceAccountNamespace},
			"audience":                         audience,
			"token_policies":                   []string{"reminders"},
			"token_no_default_policy":          true,
			"token_type":                       tokenType,
			"token_ttl":                        "5m",
		})
	}

	now := time.Now().UTC().Add(3 * time.Hour)
	server.writeSecret("app/expired", map[string]string{"expires_at": "2020-01-01"})
	server.writeSecret("app/due", map[string]string{"rotation_interval": "1h", "rotation_note": "  Rotate  it   in the console "})
	server.writeSecret("app/soon", map[string]string{"expires_at": now.Add(73 * time.Hour).Format(time.RFC3339)})
	server.writeSecret("app/typo", map[string]string{"rotation_intervall": "30d"})
	server.writeSecret("app/fine", map[string]string{"rotation_interval": "90d"})
	server.writeSecret("app/untracked", map[string]string{"owner": "me"})
	server.writeSecret("weird name/a#b?c%d", map[string]string{"expires_at": "2020-01-01"})
	server.writeSecret("deleted/key", map[string]string{"expires_at": "2020-01-01", "rotation_typo": "x"})
	server.mustRoot(http.MethodDelete, "secret/data/deleted/key", nil)
	server.writeSecret("destroyed/key", map[string]string{"expires_at": "2020-01-01"})
	server.mustRoot(http.MethodPost, "secret/destroy/destroyed/key", map[string]any{"versions": []int{1}})
	server.mustRoot(http.MethodPost, "secret/metadata/metadata-only/key", map[string]any{"custom_metadata": map[string]string{"expires_at": "2020-01-01"}})
	server.mustRoot(http.MethodPost, "secret/metadata/scheduled/key", map[string]any{"delete_version_after": "24h"})
	server.writeSecret("scheduled/key", map[string]string{"expires_at": "2020-01-01"})

	jwtFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwtFile, []byte(serviceAccountJWT(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	ntfy := startNtfy(t)
	environment := map[string]string{
		"OPENBAO_ADDR":     server.address,
		"OPENBAO_MOUNT":    "secret",
		"OPENBAO_K8S_ROLE": "openbao-rotation-reminders",
		"OPENBAO_CA_FILE":  server.caFile,
		"OPENBAO_JWT_FILE": jwtFile,
		"OPENBAO_UI_URL":   "https://bao.example.com",
		"NTFY_URL":         ntfy.server.URL,
		"NTFY_TOPIC":       topic,
	}
	run := func(t *testing.T, change map[string]string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		getenv := func(name string) string {
			if value, ok := change[name]; ok {
				return value
			}
			return environment[name]
		}
		status := app.Run(t.Context(), getenv, &stdout, &stderr, now)
		return status, stdout.String(), stderr.String()
	}

	t.Run("policy only allows metadata", func(t *testing.T) {
		var response struct {
			Auth struct {
				ClientToken string `json:"client_token"`
			} `json:"auth"`
		}
		content := server.mustRoot(http.MethodPost, "auth/token/create", map[string]any{"policies": []string{"reminders"}, "no_default_policy": true, "ttl": "5m"})
		if err := json.Unmarshal(content, &response); err != nil {
			t.Fatal(err)
		}
		token := response.Auth.ClientToken
		if status, _ := server.request("LIST", "secret/metadata/", token, nil); status != http.StatusOK {
			t.Errorf("list = %d", status)
		}
		if status, _ := server.request(http.MethodGet, "secret/metadata/app/expired", token, nil); status != http.StatusOK {
			t.Errorf("metadata read = %d", status)
		}
		status, body := server.request(http.MethodGet, "secret/data/app/expired", token, nil)
		if status != http.StatusForbidden || bytes.Contains(body, []byte("do-not-read")) {
			t.Errorf("secret value read = %d: %s", status, body)
		}
		if status, _ := server.request(http.MethodPost, "secret/metadata/app/expired", token, map[string]any{"custom_metadata": map[string]string{}}); status != http.StatusForbidden {
			t.Errorf("metadata write = %d", status)
		}
	})

	t.Run("digest", func(t *testing.T) {
		before := server.tokenCount()
		status, stdout, stderr := run(t, nil)
		if status != 0 || stderr != "" {
			t.Fatalf("status %d\nstdout:\n%s\nstderr:\n%s", status, stdout, stderr)
		}
		if after := server.tokenCount(); after != before {
			t.Errorf("tokens went from %d to %d", before, after)
		}

		title := "OpenBao: 3 expired, 1 due, 1 expiring soon, 1 invalid"
		if !strings.HasPrefix(stdout, "scanned=11 tracked=7 findings=6\n--- "+title+" (priority=urgent) ---\n") {
			t.Errorf("stdout:\n%s", stdout)
		}
		notifications := ntfy.take()
		if len(notifications) != 1 {
			t.Fatalf("received %d notifications", len(notifications))
		}
		message := notifications[0]
		if message.path != "/"+topic {
			t.Errorf("path = %s", message.path)
		}
		for header, want := range map[string]string{
			"Title":    title,
			"Priority": "urgent",
			"Tags":     "rotating_light,key",
			"Click":    "https://bao.example.com",
		} {
			if got := message.header.Get(header); got != want {
				t.Errorf("%s = %q, want %q", header, got, want)
			}
		}
		if !strings.HasSuffix(stdout, message.body+"\n") {
			t.Errorf("the log and notification bodies differ:\n%s\n---\n%s", stdout, message.body)
		}

		sections := strings.Split(message.body, "\n\n")
		if len(sections) != 4 {
			t.Fatalf("body:\n%s", message.body)
		}
		expected := []string{
			"EXPIRED:\n- app/expired: expired 2020-01-01 (",
			"\n- scheduled/key: expired 2020-01-01 (",
			"\n- weird name/a#b?c%d: expired 2020-01-01 (",
			"DUE FOR ROTATION:\n- app/due: due ",
			" (1h overdue, interval 1h)\n  Note: Rotate it in the console",
			"EXPIRING SOON:\n- app/soon: expires " + now.Add(73*time.Hour).Format(time.DateOnly) + " (in 3d)",
			"INVALID METADATA:\n- app/typo: metadata error: unknown metadata key \"rotation_intervall\"",
		}
		for _, want := range expected {
			if !strings.Contains(message.body, want) {
				t.Errorf("body is missing %q:\n%s", want, message.body)
			}
		}
		for _, skipped := range []string{"deleted/key", "destroyed/key", "metadata-only/key", "app/fine", "app/untracked"} {
			if strings.Contains(message.body, skipped) {
				t.Errorf("body mentions %s:\n%s", skipped, message.body)
			}
		}
	})

	t.Run("batch token", func(t *testing.T) {
		status, stdout, stderr := run(t, map[string]string{"OPENBAO_K8S_ROLE": "batch"})
		if status != 0 || stderr != "" || !strings.HasPrefix(stdout, "scanned=11 tracked=7 findings=6\n") {
			t.Fatalf("status %d\nstdout:\n%s\nstderr:\n%s", status, stdout, stderr)
		}
		if len(ntfy.take()) != 1 {
			t.Error("expected one notification")
		}
	})

	t.Run("without the CA", func(t *testing.T) {
		status, stdout, stderr := run(t, map[string]string{"OPENBAO_CA_FILE": ""})
		if status != 1 || stdout != "" || !strings.HasPrefix(stderr, "openbao error: request failed calling POST /v1/auth/kubernetes/login: tls: failed to verify certificate: ") {
			t.Fatalf("status %d\nstdout:\n%s\nstderr:\n%s", status, stdout, stderr)
		}
	})

	t.Run("unknown role", func(t *testing.T) {
		status, _, stderr := run(t, map[string]string{"OPENBAO_K8S_ROLE": "missing"})
		if status != 1 || stderr != "openbao error: HTTP 400 calling POST /v1/auth/kubernetes/login\n" {
			t.Fatalf("status %d, stderr %q", status, stderr)
		}
	})

	t.Run("mount the policy does not cover", func(t *testing.T) {
		server.mustRoot(http.MethodPost, "sys/mounts/other", map[string]any{"type": "kv", "options": map[string]string{"version": "2"}})
		before := server.tokenCount()
		status, _, stderr := run(t, map[string]string{"OPENBAO_MOUNT": "other"})
		if status != 1 || stderr != "openbao error: HTTP 403 calling GET /v1/other/metadata/\n" {
			t.Fatalf("status %d, stderr %q", status, stderr)
		}
		if after := server.tokenCount(); after != before {
			t.Errorf("tokens went from %d to %d after a failed run", before, after)
		}
	})
}
