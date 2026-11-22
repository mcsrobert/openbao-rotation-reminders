package httpclient

import (
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClientTrustsTheCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	untrusted, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := untrusted.Get(server.URL)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("a server signed by an unknown CA was trusted")
	}

	roots, err := LoadCertPool(writeFile(t, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := New(roots)
	if err != nil {
		t.Fatal(err)
	}
	response, err = trusted.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestClientRequiresTLS12(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	server.StartTLS()
	defer server.Close()

	roots, err := LoadCertPool(writeFile(t, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(roots)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("connected with TLS 1.1")
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			t.Error("the redirect was followed")
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()

	client, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestLoadCertPoolErrors(t *testing.T) {
	if _, err := LoadCertPool(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.HasPrefix(err.Error(), "read CA file: ") {
		t.Errorf("missing file: %v", err)
	}
	path := writeFile(t, []byte("not a certificate"))
	if _, err := LoadCertPool(path); err == nil || err.Error() != "no certificates found in "+path {
		t.Errorf("no certificates: %v", err)
	}
}
