// Package app runs one check: it reads the metadata of every secret in a KV
// v2 mount and publishes a digest of the findings to ntfy.
package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/config"
	"github.com/mcsrobert/openbao-rotation-reminders/internal/digest"
	"github.com/mcsrobert/openbao-rotation-reminders/internal/httpclient"
	"github.com/mcsrobert/openbao-rotation-reminders/internal/ntfy"
	"github.com/mcsrobert/openbao-rotation-reminders/internal/openbao"
	"github.com/mcsrobert/openbao-rotation-reminders/internal/reminder"
)

var (
	retryFor   = 30 * time.Second
	retryDelay = time.Second
)

// Run checks every secret at now, with the configuration from getenv, and
// returns the exit status.
func Run(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer, now time.Time) int {
	cfg, bao, publisher, err := setup(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}

	result, err := scan(ctx, cfg, bao, now, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "openbao error: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "scanned=%d tracked=%d findings=%d\n", result.scanned, result.tracked, len(result.findings))
	if len(result.findings) == 0 {
		return 0
	}

	notification := digest.Build(result.findings)
	_, _ = fmt.Fprintf(stdout, "--- %s (priority=%s) ---\n%s\n", notification.Title, notification.Priority, notification.Body)
	err = retryConnect(ctx, func() error {
		return publisher.Publish(ctx, ntfy.Message{
			Title:    notification.Title,
			Priority: notification.Priority,
			Tags:     notification.Tags,
			Click:    cfg.UIURL,
			Body:     digest.Truncate(notification.Body),
		})
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "notification error: %v\n", err)
		return 1
	}
	return 0
}

func setup(getenv func(string) string) (*config.Config, *openbao.Client, *ntfy.Client, error) {
	cfg, err := config.Load(getenv)
	if err != nil {
		return nil, nil, nil, err
	}
	var roots *x509.CertPool
	if cfg.CAFile != "" {
		if roots, err = httpclient.LoadCertPool(cfg.CAFile); err != nil {
			return nil, nil, nil, fmt.Errorf("OPENBAO_CA_FILE: %w", err)
		}
	}
	baoHTTP, err := httpclient.New(roots)
	if err != nil {
		return nil, nil, nil, err
	}
	bao, err := openbao.New(cfg.OpenBaoAddress, cfg.OpenBaoMount, baoHTTP)
	if err != nil {
		return nil, nil, nil, err
	}
	ntfyHTTP, err := httpclient.New(nil)
	if err != nil {
		return nil, nil, nil, err
	}
	publisher, err := ntfy.New(cfg.NtfyURL, cfg.NtfyTopic, ntfyHTTP)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, bao, publisher, nil
}

type result struct {
	scanned  int
	tracked  int
	findings []reminder.Finding
}

// scan logs in, evaluates every secret and revokes the token again, even when
// the scan fails. A failed revocation is only a warning, because the token
// expires on its own.
func scan(ctx context.Context, cfg *config.Config, bao *openbao.Client, now time.Time, stderr io.Writer) (result, error) {
	jwt, err := readJWT(cfg.JWTFile)
	if err != nil {
		return result{}, err
	}
	if err := retryConnect(ctx, func() error { return bao.Login(ctx, cfg.Role, jwt) }); err != nil {
		return result{}, err
	}
	defer func() {
		if err := bao.Revoke(context.WithoutCancel(ctx)); err != nil {
			_, _ = fmt.Fprintf(stderr, "warning: token revocation failed: %v\n", err)
		}
	}()

	keys, err := bao.ListKeys(ctx)
	if err != nil {
		return result{}, err
	}
	var r result
	for _, key := range keys {
		r.scanned++
		metadata, err := bao.ReadMetadata(ctx, key)
		if err != nil {
			return result{}, err
		}
		if metadata == nil {
			continue
		}
		tracked, finding := reminder.Evaluate(key, metadata, now, cfg.DefaultExpiresWarnBefore)
		if tracked {
			r.tracked++
		}
		if finding != nil {
			r.findings = append(r.findings, *finding)
		}
	}
	return r, nil
}

func retryConnect(ctx context.Context, send func() error) error {
	deadline := time.Now().Add(retryFor)
	for {
		err := send()
		var opErr *net.OpError
		if err == nil || !errors.As(err, &opErr) || opErr.Op != "dial" || time.Now().Add(retryDelay).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(retryDelay):
		}
	}
}

func readJWT(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read JWT file: %w", err)
	}
	jwt := strings.TrimSpace(string(content))
	if jwt == "" {
		return "", fmt.Errorf("JWT file %s is empty", path)
	}
	return jwt, nil
}
