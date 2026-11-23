// Package config reads the configuration from environment variables.
package config

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mcsrobert/openbao-rotation-reminders/internal/reminder"
)

const (
	defaultJWTFile           = "/var/run/secrets/openbao/token"
	defaultNtfyURL           = "https://ntfy.sh"
	defaultExpiresWarnBefore = "14d"
)

// topicPattern is the topic name ntfy accepts for publishing.
var topicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// Config is the configuration of one run.
type Config struct {
	OpenBaoAddress           string
	OpenBaoMount             string
	Role                     string
	CAFile                   string
	JWTFile                  string
	UIURL                    string
	NtfyURL                  string
	NtfyTopic                string
	DefaultExpiresWarnBefore time.Duration
}

// Load reads the configuration with getenv. Unset and empty variables are
// treated the same, and values are trimmed of surrounding whitespace.
func Load(getenv func(string) string) (*Config, error) {
	get := func(name, fallback string) string {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			return value
		}
		return fallback
	}
	cfg := &Config{
		OpenBaoAddress: get("OPENBAO_ADDR", ""),
		OpenBaoMount:   strings.Trim(get("OPENBAO_MOUNT", ""), "/"),
		Role:           get("OPENBAO_K8S_ROLE", ""),
		CAFile:         get("OPENBAO_CA_FILE", ""),
		JWTFile:        get("OPENBAO_JWT_FILE", defaultJWTFile),
		UIURL:          get("OPENBAO_UI_URL", ""),
		NtfyURL:        get("NTFY_URL", defaultNtfyURL),
		NtfyTopic:      get("NTFY_TOPIC", ""),
	}

	var errs []string
	add := func(name string, err error) {
		if err != nil {
			errs = append(errs, name+": "+err.Error())
		}
	}
	add("OPENBAO_ADDR", validateURL(cfg.OpenBaoAddress, true))
	add("OPENBAO_MOUNT", validateMount(cfg.OpenBaoMount))
	if cfg.Role == "" {
		add("OPENBAO_K8S_ROLE", errors.New("required"))
	}
	if cfg.UIURL != "" {
		add("OPENBAO_UI_URL", validateURL(cfg.UIURL, false))
	}
	add("NTFY_URL", validateURL(cfg.NtfyURL, true))
	// The topic is never part of an error, because knowing it is enough to
	// publish and read.
	if !topicPattern.MatchString(cfg.NtfyTopic) {
		errs = append(errs, "NTFY_TOPIC must be 1 to 64 letters, digits, hyphens or underscores")
	}
	warn, err := reminder.ParseDuration(get("DEFAULT_EXPIRES_WARN_BEFORE", defaultExpiresWarnBefore), true)
	add("DEFAULT_EXPIRES_WARN_BEFORE", err)
	cfg.DefaultExpiresWarnBefore = warn

	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return cfg, nil
}

// validateURL accepts an http or https URL with a host and no credentials.
// With base, the URL is a prefix for request paths, so it must not have a
// query or fragment either.
func validateURL(value string, base bool) error {
	if value == "" {
		return errors.New("required")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return errors.New("host is required")
	}
	if parsed.User != nil {
		return errors.New("must not contain credentials")
	}
	if base && (parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "") {
		return errors.New("must not contain a query or a fragment")
	}
	return nil
}

func validateMount(mount string) error {
	if mount == "" {
		return errors.New("required")
	}
	for segment := range strings.SplitSeq(mount, "/") {
		switch segment {
		case "":
			return errors.New("must not contain repeated slashes")
		case ".", "..":
			return errors.New("must not contain . or .. segments")
		}
	}
	return nil
}
