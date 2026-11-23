// Package ntfy publishes notifications to a private ntfy topic.
package ntfy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const maxResponseSize = 1024

// Message is one notification.
type Message struct {
	Title    string
	Priority string
	Tags     string
	// Click is a URL that opens when the notification is tapped.
	Click string
	Body  string
}

// Client publishes to one topic. The topic name is the only secret protecting
// it, so errors never contain the topic or the request URL.
type Client struct {
	target string
	http   *http.Client
}

// New returns a client for topic on the server at serverURL.
func New(serverURL, topic string, client *http.Client) (*Client, error) {
	base, err := url.Parse(serverURL)
	if err != nil {
		return nil, errors.New("NTFY_URL is not a valid URL")
	}
	return &Client{target: base.JoinPath(url.PathEscape(topic)).String(), http: client}, nil
}

// Publish sends message.
func (c *Client) Publish(ctx context.Context, message Message) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.target, strings.NewReader(message.Body))
	if err != nil {
		return errors.New("ntfy publish failed: could not build the request")
	}
	request.Header.Set("Title", encodeHeader(message.Title))
	request.Header.Set("Priority", message.Priority)
	request.Header.Set("Tags", message.Tags)
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if message.Click != "" {
		request.Header.Set("Click", message.Click)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return transportError(err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseSize))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("ntfy publish failed with HTTP %d", response.StatusCode)
	}
	return nil
}

// transportError describes a failed request without the topic. A url.Error
// names the request URL, and other errors can quote what the server sent, such
// as a malformed redirect location, so only errors of known kinds keep their
// text.
func transportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var authorityErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var recordErr tls.RecordHeaderError
	switch {
	case errors.As(err, &netErr), errors.As(err, &certErr), errors.As(err, &authorityErr),
		errors.As(err, &hostnameErr), errors.As(err, &recordErr),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("ntfy publish failed: %w", err)
	}
	return errors.New("ntfy publish failed: the request could not be completed")
}

// encodeHeader replaces control characters with a question mark and encodes
// anything else outside printable ASCII as an RFC 2047 encoded word, which
// ntfy decodes, because header values are not reliably decoded as UTF-8.
func encodeHeader(value string) string {
	return mime.BEncoding.Encode("utf-8", strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, value))
}
