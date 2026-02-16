package npm

import (
	"encoding/json"
	"fmt"
	"os"
)

type Manifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read package.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}
	return &m, nil
}

func (m *Manifest) RequestedRanges() map[string]string {
	out := map[string]string{}
	for k, v := range m.Dependencies {
		out[k] = v
	}
	for k, v := range m.OptionalDependencies {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	for k, v := range m.DevDependencies {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}
