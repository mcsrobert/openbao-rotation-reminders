// Package httpclient builds the HTTP clients used for OpenBao and ntfy.
package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

const requestTimeout = 10 * time.Second

// New returns a client that requires TLS 1.2 or later and trusts roots, or the
// system roots when roots is nil.
//
// The client never follows redirects: it returns the redirect response, which
// callers treat as a failure. Following one would resend the OpenBao token or
// the private ntfy topic to whichever location the redirect names.
func New(roots *x509.CertPool) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("unexpected default transport")
	}
	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// LoadCertPool reads the PEM certificates in path.
func LoadCertPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in %s", path)
	}
	return pool, nil
}
