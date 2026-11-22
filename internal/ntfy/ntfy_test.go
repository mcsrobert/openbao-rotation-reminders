package ntfy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/httpclient"
)

const topic = "private-topic-4f2a9c"

func newClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	httpClient, err := httpclient.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(serverURL, topic, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestPublish(t *testing.T) {
	var request *http.Request
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := io.ReadAll(r.Body)
		request, body = r, string(content)
	}))
	defer server.Close()

	err := newClient(t, server.URL+"/").Publish(t.Context(), Message{
		Title:    "OpenBao: clé expired\n",
		Priority: "urgent",
		Tags:     "rotating_light,key",
		Click:    "https://bao.example.com",
		Body:     "EXPIRED:\n- clé: expired",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != http.MethodPost || request.URL.Path != "/"+topic {
		t.Errorf("request = %s %s", request.Method, request.URL.Path)
	}
	for header, want := range map[string]string{
		"Title":        "OpenBao: cl? expired?",
		"Priority":     "urgent",
		"Tags":         "rotating_light,key",
		"Click":        "https://bao.example.com",
		"Content-Type": "text/plain; charset=utf-8",
	} {
		if got := request.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if body != "EXPIRED:\n- clé: expired" {
		t.Errorf("body = %q", body)
	}
}

func TestPublishWithoutClick(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Click"]; ok {
			t.Error("unexpected Click header")
		}
	}))
	defer server.Close()
	if err := newClient(t, server.URL).Publish(t.Context(), Message{Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
}

func TestPublishEscapesTheTopic(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
	}))
	defer server.Close()
	httpClient, err := httpclient.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(server.URL+"/base", "a%b\x7f", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(t.Context(), Message{}); err != nil {
		t.Fatal(err)
	}
	if path != "/base/a%25b%7F" {
		t.Errorf("path = %q", path)
	}
}

func TestPublishRefusesRedirects(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	err := newClient(t, server.URL).Publish(t.Context(), Message{Body: "b"})
	if err == nil || err.Error() != "ntfy publish failed with HTTP 307" {
		t.Fatalf("error = %v", err)
	}
	if followed.Load() {
		t.Error("the redirect was followed")
	}
}

func TestPublishErrorsDoNotRevealTheTopic(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden: "+r.URL.String(), http.StatusForbidden)
	}))
	defer failing.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + listener.Addr().String()
	_ = listener.Close()

	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, _ := w.(http.Hijacker)
		conn, _, _ := hijacker.Hijack()
		_ = conn.Close()
	}))
	defer hanging.Close()

	tests := []struct {
		name    string
		server  string
		message string
	}{
		{"HTTP error", failing.URL, "ntfy publish failed with HTTP 403"},
		{"connection refused", closed, "ntfy publish failed: dial tcp"},
		{"connection closed", hanging.URL, "ntfy publish failed: "},
		{"TLS error", strings.Replace(failing.URL, "http://", "https://", 1), "ntfy publish failed: "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newClient(t, test.server).Publish(t.Context(), Message{Body: "b"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.HasPrefix(err.Error(), test.message) {
				t.Errorf("error = %q, want prefix %q", err, test.message)
			}
			if strings.Contains(err.Error(), topic) || strings.Contains(err.Error(), test.server) {
				t.Errorf("error reveals the topic or URL: %q", err)
			}
		})
	}
}
