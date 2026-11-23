package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const topic = "private-topic-4f2a9c"

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func minimal() map[string]string {
	return map[string]string{
		"OPENBAO_ADDR":     "https://openbao.security.svc.cluster.local:8200",
		"OPENBAO_MOUNT":    "homelab",
		"OPENBAO_K8S_ROLE": "openbao-rotation-reminders",
		"NTFY_TOPIC":       topic,
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(minimal()))
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		OpenBaoAddress:           "https://openbao.security.svc.cluster.local:8200",
		OpenBaoMount:             "homelab",
		Role:                     "openbao-rotation-reminders",
		JWTFile:                  "/var/run/secrets/openbao/token",
		NtfyURL:                  "https://ntfy.sh",
		NtfyTopic:                topic,
		DefaultExpiresWarnBefore: 14 * 24 * time.Hour,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
}

func TestLoadEverything(t *testing.T) {
	values := map[string]string{
		"OPENBAO_ADDR":                " http://127.0.0.1:8200/ ",
		"OPENBAO_MOUNT":               "/kv/homelab/",
		"OPENBAO_K8S_ROLE":            "role",
		"OPENBAO_CA_FILE":             "/etc/openbao/ca.crt",
		"OPENBAO_JWT_FILE":            "/token",
		"OPENBAO_UI_URL":              "https://bao.example.com/ui/vault/secrets?x=1#kv",
		"NTFY_URL":                    "http://ntfy.example.com/base",
		"NTFY_TOPIC":                  " " + topic + "\n",
		"DEFAULT_EXPIRES_WARN_BEFORE": "0d",
	}
	cfg, err := Load(env(values))
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		OpenBaoAddress: "http://127.0.0.1:8200/",
		OpenBaoMount:   "kv/homelab",
		Role:           "role",
		CAFile:         "/etc/openbao/ca.crt",
		JWTFile:        "/token",
		UIURL:          "https://bao.example.com/ui/vault/secrets?x=1#kv",
		NtfyURL:        "http://ntfy.example.com/base",
		NtfyTopic:      topic,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"missing address", "OPENBAO_ADDR", "", "OPENBAO_ADDR: required"},
		{"address scheme", "OPENBAO_ADDR", "ftp://openbao", "OPENBAO_ADDR: scheme must be http or https"},
		{"address without host", "OPENBAO_ADDR", "https://", "OPENBAO_ADDR: host is required"},
		{"address with only a port", "OPENBAO_ADDR", "https://:8200", "OPENBAO_ADDR: host is required"},
		{"UI URL with only a port", "OPENBAO_UI_URL", "https://:8200/ui", "OPENBAO_UI_URL: host is required"},
		{"address with credentials", "OPENBAO_ADDR", "https://user:pass@openbao", "OPENBAO_ADDR: must not contain credentials"},
		{"address with query", "OPENBAO_ADDR", "https://openbao?x=1", "OPENBAO_ADDR: must not contain a query or a fragment"},
		{"address not a URL", "OPENBAO_ADDR", "https://open bao:x", "OPENBAO_ADDR: not a valid URL"},
		{"missing mount", "OPENBAO_MOUNT", "/", "OPENBAO_MOUNT: required"},
		{"mount with dot segment", "OPENBAO_MOUNT", "a/../b", "OPENBAO_MOUNT: must not contain . or .. segments"},
		{"mount with repeated slash", "OPENBAO_MOUNT", "a//b", "OPENBAO_MOUNT: must not contain repeated slashes"},
		{"missing role", "OPENBAO_K8S_ROLE", " ", "OPENBAO_K8S_ROLE: required"},
		{"UI URL", "OPENBAO_UI_URL", "javascript:alert(1)", "OPENBAO_UI_URL: scheme must be http or https"},
		{"ntfy URL", "NTFY_URL", "ntfy.sh", "NTFY_URL: scheme must be http or https"},
		{"ntfy URL with credentials", "NTFY_URL", "https://user:" + topic + "@ntfy.sh", "NTFY_URL: must not contain credentials"},
		{"ntfy URL with fragment", "NTFY_URL", "https://ntfy.sh/#x", "NTFY_URL: must not contain a query or a fragment"},
		{"missing topic", "NTFY_TOPIC", "", "NTFY_TOPIC must be 1 to 64 letters, digits, hyphens or underscores"},
		{"warning", "DEFAULT_EXPIRES_WARN_BEFORE", "2 weeks", `DEFAULT_EXPIRES_WARN_BEFORE: invalid duration "2 weeks" (use e.g. 80d, 12w, 24h)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := minimal()
			values[test.key] = test.value
			_, err := Load(env(values))
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsTopicsNtfyDoesNotAccept(t *testing.T) {
	for _, value := range []string{"a/b", "a b", "a\tb", "a\rb", "a\nb", "a?b", "a&b", "a#b", ".", "..", "a.b", "clé", strings.Repeat("a", 65)} {
		values := minimal()
		values["NTFY_TOPIC"] = value
		_, err := Load(env(values))
		if err == nil || err.Error() != "NTFY_TOPIC must be 1 to 64 letters, digits, hyphens or underscores" {
			t.Errorf("topic %q: error = %v", value, err)
		}
	}
}

func TestLoadAcceptsTheLongestTopic(t *testing.T) {
	values := minimal()
	values["NTFY_TOPIC"] = "A-z_0" + strings.Repeat("9", 59)
	if _, err := Load(env(values)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReportsEveryError(t *testing.T) {
	_, err := Load(env(map[string]string{"NTFY_TOPIC": topic + "/x"}))
	want := "OPENBAO_ADDR: required; OPENBAO_MOUNT: required; OPENBAO_K8S_ROLE: required; NTFY_TOPIC must be 1 to 64 letters, digits, hyphens or underscores"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if strings.Contains(err.Error(), topic) {
		t.Error("the error contains the topic")
	}
}
