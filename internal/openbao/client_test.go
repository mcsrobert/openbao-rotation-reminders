package openbao

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/httpclient"
)

// serve starts a fake OpenBao that fails the test if anything reads a KV data
// path, and records each request as "METHOD path?query token".
func serve(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/data/") {
			t.Errorf("request for a secret value: %s", r.URL.Path)
		}
		mu.Lock()
		requests = append(requests, strings.TrimSpace(fmt.Sprintf("%s %s %s", r.Method, r.URL.RequestURI(), r.Header.Get("X-Vault-Token"))))
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(requests)
	}
}

func newClient(t *testing.T, address string) *Client {
	t.Helper()
	httpClient, err := httpclient.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(address, "kv/homelab", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// notFound responds like OpenBao does for a missing key or an empty
// directory.
func notFound(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"errors":[]}`)
}

func login(tokenType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/kubernetes/login" {
			_, _ = fmt.Fprintf(w, `{"auth":{"client_token":"s.token","token_type":%q}}`, tokenType)
		}
	}
}

func TestLogin(t *testing.T) {
	var body map[string]string
	var contentType string
	server, requests := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/kubernetes/login":
			contentType = r.Header.Get("Content-Type")
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `{"auth":{"client_token":"s.token","token_type":"service"}}`)
		default:
			_, _ = io.WriteString(w, `{"data":{"keys":[]}}`)
		}
	})
	client := newClient(t, server.URL)
	if err := client.Login(t.Context(), "reminders", "jwt-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListKeys(t.Context()); err != nil {
		t.Fatal(err)
	}
	if body["role"] != "reminders" || body["jwt"] != "jwt-value" || len(body) != 2 {
		t.Errorf("login body = %v", body)
	}
	if contentType != "application/json" {
		t.Errorf("content type = %q", contentType)
	}
	want := []string{
		"POST /v1/auth/kubernetes/login",
		"GET /v1/kv/homelab/metadata/?list=true s.token",
	}
	if got := requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func TestLoginFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"no token", http.StatusOK, `{"auth":{"client_token":""}}`, "kubernetes login response missing client_token"},
		{"no auth", http.StatusOK, `{"auth":null}`, "kubernetes login response missing client_token"},
		{"denied", http.StatusForbidden, `{"errors":["permission denied: secret-detail"]}`, "HTTP 403 calling POST /v1/auth/kubernetes/login"},
		{"not JSON", http.StatusOK, `secret-detail`, "request failed calling POST /v1/auth/kubernetes/login: response is not valid JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})
			err := newClient(t, server.URL).Login(t.Context(), "role", "jwt")
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRevoke(t *testing.T) {
	t.Run("service token", func(t *testing.T) {
		server, requests := serve(t, login("service"))
		client := newClient(t, server.URL)
		if err := client.Revoke(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := client.Login(t.Context(), "role", "jwt"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := client.Revoke(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		want := []string{"POST /v1/auth/kubernetes/login", "POST /v1/auth/token/revoke-self s.token"}
		if got := requests(); !slices.Equal(got, want) {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})

	t.Run("batch token", func(t *testing.T) {
		server, requests := serve(t, login("batch"))
		client := newClient(t, server.URL)
		if err := client.Login(t.Context(), "role", "jwt"); err != nil {
			t.Fatal(err)
		}
		if err := client.Revoke(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := requests(); !slices.Equal(got, []string{"POST /v1/auth/kubernetes/login"}) {
			t.Errorf("requests = %q", got)
		}
	})

	t.Run("failure", func(t *testing.T) {
		server, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/auth/token/revoke-self" {
				http.Error(w, "secret-detail", http.StatusForbidden)
				return
			}
			login("service")(w, r)
		})
		client := newClient(t, server.URL)
		if err := client.Login(t.Context(), "role", "jwt"); err != nil {
			t.Fatal(err)
		}
		err := client.Revoke(t.Context())
		if err == nil || err.Error() != "HTTP 403 calling POST /v1/auth/token/revoke-self" {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestListKeys(t *testing.T) {
	listings := map[string]string{
		"/v1/kv/homelab/metadata/":                       `{"data":{"keys":["b","a/","weird name/","empty/"]}}`,
		"/v1/kv/homelab/metadata/a/":                     `{"data":{"keys":["z","nested/"]}}`,
		"/v1/kv/homelab/metadata/a/nested/":              `{"data":{"keys":["deep"]}}`,
		"/v1/kv/homelab/metadata/weird%20name/":          `{"data":{"keys":["a#b?c%d","x:y@z"]}}`,
		"/v1/kv/homelab/metadata/weird%20name/a%23b%3Fc": `{"data":{"keys":["wrong"]}}`,
	}
	server, requests := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "list=true" || r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
		listing, ok := listings[r.URL.EscapedPath()]
		if !ok {
			notFound(w, r)
			return
		}
		_, _ = io.WriteString(w, listing)
	})
	keys, err := newClient(t, server.URL).ListKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/nested/deep", "a/z", "b", "weird name/a#b?c%d", "weird name/x:y@z"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %q, want %q", keys, want)
	}
	if got := len(requests()); got != 5 {
		t.Errorf("made %d requests, want 5", got)
	}
}

func TestListKeysOfAnEmptyMount(t *testing.T) {
	server, _ := serve(t, notFound)
	keys, err := newClient(t, server.URL).ListKeys(t.Context())
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys = %q, error = %v", keys, err)
	}
}

func TestListKeysRejectsOtherNotFoundResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing mount", `{"errors":["no handler for route \"kv/homelab/metadata/\". route entry not found."]}`},
		{"no errors array", `{}`},
		{"not JSON", "404 page not found"},
		{"empty", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, test.body)
			})
			_, err := newClient(t, server.URL).ListKeys(t.Context())
			want := "HTTP 404 calling GET /v1/kv/homelab/metadata/: no such route, check OPENBAO_ADDR and OPENBAO_MOUNT"
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

func TestListKeysRejectsAListingWithoutKeys(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":{}}`, `{"data":{"keys":null}}`} {
		t.Run(body, func(t *testing.T) {
			server, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			})
			_, err := newClient(t, server.URL).ListKeys(t.Context())
			if err == nil || err.Error() != `listing of "" returned no data.keys` {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestListKeysRejectsEntriesOutsideTheMetadataPath(t *testing.T) {
	for _, entry := range []string{"..", "../", ".", "./", "", "/", "a/b", "../data/x"} {
		t.Run(entry, func(t *testing.T) {
			server, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"data":{"keys":[%q]}}`, entry)
			})
			_, err := newClient(t, server.URL).ListKeys(t.Context())
			if err == nil || !strings.Contains(err.Error(), "invalid entry") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadMetadata(t *testing.T) {
	server, requests := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/v1/kv/homelab/metadata/app/a%23b":
			_, _ = io.WriteString(w, `{"data":{"current_version":2,"versions":{"2":{"created_time":"2026-01-02T03:04:05.123456789Z","deletion_time":"","destroyed":false}},"custom_metadata":{"rotation_interval":"90d"}}}`)
		case "/v1/kv/homelab/metadata/app/empty":
			_, _ = io.WriteString(w, `{"data":null}`)
		default:
			notFound(w, r)
		}
	})
	client := newClient(t, server.URL)

	metadata, err := client.ReadMetadata(t.Context(), "app/a#b")
	if err != nil {
		t.Fatal(err)
	}
	if *metadata.CurrentVersion != 2 || metadata.Versions["2"].CreatedTime != "2026-01-02T03:04:05.123456789Z" || string(metadata.CustomMetadata) != `{"rotation_interval":"90d"}` {
		t.Errorf("metadata = %+v", metadata)
	}

	for _, key := range []string{"app/empty", "app/missing"} {
		if metadata, err := client.ReadMetadata(t.Context(), key); metadata != nil || err != nil {
			t.Errorf("ReadMetadata(%q) = %+v, %v", key, metadata, err)
		}
	}
	want := []string{
		"GET /v1/kv/homelab/metadata/app/a%23b",
		"GET /v1/kv/homelab/metadata/app/empty",
		"GET /v1/kv/homelab/metadata/app/missing",
	}
	if got := requests(); !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func TestErrorsDoNotIncludeResponseBodies(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusInternalServerError, `{"errors":["secret-detail"]}`, "HTTP 500 calling GET /v1/kv/homelab/metadata/app/key"},
		{http.StatusForbidden, `{"errors":["secret-detail"]}`, "HTTP 403 calling GET /v1/kv/homelab/metadata/app/key"},
		{http.StatusOK, `{"data": secret-detail`, "request failed calling GET /v1/kv/homelab/metadata/app/key: response is not valid JSON"},
		{http.StatusOK, `{"data":{"current_version":"secret-detail"}}`, "request failed calling GET /v1/kv/homelab/metadata/app/key: response is not valid JSON"},
	}
	for _, test := range tests {
		server, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(test.status)
			_, _ = io.WriteString(w, test.body)
		})
		_, err := newClient(t, server.URL).ReadMetadata(t.Context(), "app/key")
		if err == nil || err.Error() != test.want {
			t.Errorf("error = %v, want %q", err, test.want)
		}
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	var leaked atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		leaked.Store(true)
	}))
	defer elsewhere.Close()
	server, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/kubernetes/login" {
			login("service")(w, r)
			return
		}
		http.Redirect(w, r, elsewhere.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
	client := newClient(t, server.URL)
	if err := client.Login(t.Context(), "role", "jwt"); err != nil {
		t.Fatal(err)
	}
	_, err := client.ListKeys(t.Context())
	if err == nil || err.Error() != "HTTP 307 calling GET /v1/kv/homelab/metadata/" {
		t.Fatalf("error = %v", err)
	}
	if leaked.Load() {
		t.Error("the redirect was followed")
	}
}

func TestNetworkErrors(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	_ = listener.Close()

	_, err = newClient(t, address).ListKeys(t.Context())
	if err == nil || !strings.HasPrefix(err.Error(), "request failed calling GET /v1/kv/homelab/metadata/: dial tcp ") {
		t.Fatalf("error = %v", err)
	}
}

func TestAddressWithAPathPrefix(t *testing.T) {
	server, requests := serve(t, notFound)
	if _, err := newClient(t, server.URL+"/bao/").ListKeys(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := requests(); !slices.Equal(got, []string{"GET /bao/v1/kv/homelab/metadata/?list=true"}) {
		t.Errorf("requests = %q", got)
	}
}
