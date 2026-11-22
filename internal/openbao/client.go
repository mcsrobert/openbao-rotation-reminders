// Package openbao is a minimal client for OpenBao Kubernetes login and KV v2
// metadata.
//
// It only ever calls the login, token revocation and metadata endpoints, so it
// has no way to read secret values.
package openbao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const maxResponseSize = 8 << 20

// Metadata is the part of a KV v2 metadata response that reminders use.
type Metadata struct {
	CurrentVersion *int64              `json:"current_version"`
	Versions       map[string]*Version `json:"versions"`
	// CustomMetadata is kept raw, so that a malformed value can be reported
	// instead of failing the whole read.
	CustomMetadata json.RawMessage `json:"custom_metadata"`
}

// Version describes one version of a secret.
type Version struct {
	CreatedTime  string `json:"created_time"`
	DeletionTime string `json:"deletion_time"`
	Destroyed    bool   `json:"destroyed"`
}

// Client talks to a single OpenBao server and KV v2 mount. It holds the token
// from Login until Revoke.
type Client struct {
	base      *url.URL
	mount     string
	http      *http.Client
	token     string
	revocable bool
}

// New returns a client for the KV v2 mount at address.
func New(address, mount string, client *http.Client) (*Client, error) {
	base, err := url.Parse(address)
	if err != nil {
		return nil, errors.New("OPENBAO_ADDR is not a valid URL")
	}
	if base.Path == "" {
		base.Path = "/"
	}
	return &Client{base: base, mount: mount, http: client}, nil
}

// Login logs in with the Kubernetes auth method and keeps the token for later
// requests.
func (c *Client) Login(ctx context.Context, role, jwt string) error {
	var response struct {
		Auth *struct {
			ClientToken string `json:"client_token"`
			TokenType   string `json:"token_type"`
		} `json:"auth"`
	}
	target := c.base.JoinPath("v1", "auth", "kubernetes", "login")
	body := map[string]string{"role": role, "jwt": jwt}
	if _, err := c.do(ctx, http.MethodPost, target, body, &response, false); err != nil {
		return err
	}
	if response.Auth == nil || response.Auth.ClientToken == "" {
		return errors.New("kubernetes login response missing client_token")
	}
	c.token = response.Auth.ClientToken
	c.revocable = response.Auth.TokenType != "batch"
	return nil
}

// Revoke revokes the token from Login with its own token and forgets it. It
// does nothing before Login and for batch tokens, which cannot be revoked.
func (c *Client) Revoke(ctx context.Context) error {
	if c.token == "" || !c.revocable {
		return nil
	}
	defer func() { c.token = "" }()
	_, err := c.do(ctx, http.MethodPost, c.base.JoinPath("v1", "auth", "token", "revoke-self"), nil, nil, false)
	return err
}

// ListKeys returns every secret path in the mount, sorted.
func (c *Client) ListKeys(ctx context.Context) ([]string, error) {
	var keys []string
	pending := []string{""}
	for len(pending) > 0 {
		directory := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		var response struct {
			Data *struct {
				Keys []string `json:"keys"`
			} `json:"data"`
		}
		target := c.metadataURL(directory)
		target.RawQuery = "list=true"
		found, err := c.do(ctx, http.MethodGet, target, nil, &response, true)
		if err != nil {
			return nil, err
		}
		if !found || response.Data == nil {
			continue
		}
		for _, entry := range response.Data.Keys {
			if !validEntry(entry) {
				return nil, fmt.Errorf("listing of %q returned an invalid entry %q", directory, entry)
			}
			if strings.HasSuffix(entry, "/") {
				pending = append(pending, directory+entry)
			} else {
				keys = append(keys, directory+entry)
			}
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// ReadMetadata returns the metadata of key, or nil if it does not exist.
func (c *Client) ReadMetadata(ctx context.Context, key string) (*Metadata, error) {
	var response struct {
		Data *Metadata `json:"data"`
	}
	found, err := c.do(ctx, http.MethodGet, c.metadataURL(key), nil, &response, true)
	if err != nil || !found {
		return nil, err
	}
	return response.Data, nil
}

func (c *Client) metadataURL(key string) *url.URL {
	return c.base.JoinPath("v1", escapePath(c.mount), "metadata/"+escapePath(key))
}

// do sends a request and decodes a JSON response into out. With allowNotFound,
// a 404 response returns false instead of an error.
func (c *Client) do(ctx context.Context, method string, target *url.URL, body, out any, allowNotFound bool) (bool, error) {
	call := method + " " + target.EscapedPath()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return false, fmt.Errorf("encode request calling %s: %w", call, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return false, fmt.Errorf("build request calling %s: %w", call, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("X-Vault-Token", c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return false, fmt.Errorf("request failed calling %s: %w", call, err)
	}
	defer func() { _ = response.Body.Close() }()

	// Response bodies are never included in errors, so they cannot reach logs.
	if allowNotFound && response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return false, fmt.Errorf("HTTP %d calling %s", response.StatusCode, call)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return false, fmt.Errorf("request failed calling %s: read response: %w", call, err)
	}
	if len(content) > maxResponseSize {
		return false, fmt.Errorf("request failed calling %s: response is larger than %d bytes", call, maxResponseSize)
	}
	if out == nil || len(bytes.TrimSpace(content)) == 0 {
		return true, nil
	}
	if err := json.Unmarshal(content, out); err != nil {
		return false, fmt.Errorf("request failed calling %s: response is not valid JSON", call)
	}
	return true, nil
}

// validEntry reports whether a listing entry is a single path segment, with a
// trailing slash for directories. Anything else could make a request path
// leave the metadata endpoint.
func validEntry(entry string) bool {
	name := strings.TrimSuffix(entry, "/")
	return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}

func escapePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
