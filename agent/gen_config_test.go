package agent

import (
	"strings"
	"testing"

	"agent-netx/config"
)

// TestSpecToConfig_RoundTrip exercises the spec->Config->YAML->Config path that
// the gen_config tool relies on: a realistic spec must assemble into a Config
// that both marshals and re-parses cleanly (the guarantee gen_config gives the
// LLM — "the file you wrote is immediately runnable").
func TestSpecToConfig_RoundTrip(t *testing.T) {
	spec := map[string]any{
		"listen": map[string]any{"http": 8080, "socks5": 7891},
		"mode":   "rule",
		"proxies": []any{
			map[string]any{
				"name": "ss1", "type": "ss", "server": "a.com", "port": 8388,
				"cipher": "aes-256-gcm", "password": "pw",
			},
		},
		"groups": []any{
			map[string]any{
				"name": "Auto", "type": "url-test", "proxies": []any{"ss1"},
				"url": "http://www.gstatic.com/generate_204", "interval": 300,
			},
		},
		"rules": []any{"GEOIP,CN,DIRECT", "MATCH,Auto"},
	}
	cfg, err := specToConfig(spec)
	if err != nil {
		t.Fatalf("specToConfig: %v", err)
	}
	if len(cfg.Proxies) != 1 || cfg.Proxies[0].Name != "ss1" {
		t.Fatalf("proxies = %+v", cfg.Proxies)
	}
	if cfg.Listen.HTTP != 8080 || cfg.Listen.SOCKS5 != 7891 {
		t.Fatalf("listen = %+v", cfg.Listen)
	}
	if cfg.Mode != "rule" {
		t.Fatalf("mode = %q", cfg.Mode)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].Default != "" {
		t.Fatalf("groups = %+v", cfg.Groups)
	}
	if len(cfg.Rules) != 2 {
		t.Fatalf("rules = %+v", cfg.Rules)
	}

	// Round-trip: marshal -> re-parse must succeed (gen_config's contract).
	b, err := config.YAMLMarshal(cfg)
	if err != nil {
		t.Fatalf("YAMLMarshal: %v", err)
	}
	if _, err := config.LoadFromBytes(b); err != nil {
		t.Fatalf("LoadFromBytes round-trip: %v\n--- yaml ---\n%s", err, string(b))
	}
	if !strings.Contains(string(b), "aes-256-gcm") {
		t.Fatalf("yaml missing cipher: %s", string(b))
	}
}

// TestSpecToConfig_EmptySpec verifies an empty spec still yields a valid
// (defaulted) config — the LLM may call gen_config with a minimal spec.
func TestSpecToConfig_EmptySpec(t *testing.T) {
	cfg, err := specToConfig(map[string]any{})
	if err != nil {
		t.Fatalf("specToConfig: %v", err)
	}
	if cfg.Mode != "rule" {
		t.Fatalf("mode = %q, want rule", cfg.Mode)
	}
	b, err := config.YAMLMarshal(cfg)
	if err != nil {
		t.Fatalf("YAMLMarshal: %v", err)
	}
	if _, err := config.LoadFromBytes(b); err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
}

// TestSpecToConfig_NumericCoercion confirms toInt handles JSON's float64 for
// ports, plus numeric strings ("8388").
func TestSpecToConfig_NumericCoercion(t *testing.T) {
	spec := map[string]any{
		"proxies": []any{
			map[string]any{"name": "p", "type": "ss", "port": float64(8388)}, // JSON numbers
			map[string]any{"name": "q", "type": "ss", "port": "8389"},          // string
		},
	}
	cfg, err := specToConfig(spec)
	if err != nil {
		t.Fatalf("specToConfig: %v", err)
	}
	if cfg.Proxies[0].Port != 8388 {
		t.Fatalf("port0 = %d", cfg.Proxies[0].Port)
	}
	if cfg.Proxies[1].Port != 8389 {
		t.Fatalf("port1 = %d", cfg.Proxies[1].Port)
	}
}
