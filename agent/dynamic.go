package agent

import (
	"os"
	"path/filepath"

	"agent-netx/config"
	"gopkg.in/yaml.v3"
)

// DynamicSpec is the overlay shape that the add_proxy / add_rule tools write to
// ~/.agent-netx/dynamic.yml. It is merged on top of the base config at runtime
// by config.ApplyDynamic().
type DynamicSpec struct {
	Proxies []config.ProxyConfig `yaml:"proxies"`
	Rules   []string             `yaml:"rules"`
}

func DynamicPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent-netx/dynamic.yml"
	}
	return filepath.Join(home, ".agent-netx", "dynamic.yml")
}

// LoadDynamic reads the dynamic overlay file. Returns an empty spec if the
// file does not exist.
func LoadDynamic() (*DynamicSpec, error) {
	b, err := os.ReadFile(DynamicPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &DynamicSpec{}, nil
		}
		return nil, err
	}
	spec := &DynamicSpec{}
	if err := yaml.Unmarshal(b, spec); err != nil {
		return nil, err
	}
	return spec, nil
}

// SaveDynamic overwrites the dynamic overlay file.
func SaveDynamic(spec *DynamicSpec) error {
	b, err := yaml.Marshal(spec)
	if err != nil {
		return err
	}
	dir := filepath.Dir(DynamicPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(DynamicPath(), b, 0644)
}
