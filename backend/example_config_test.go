package main

import (
	"os"
	"testing"
)

// The shipped examples must stay loadable as the config format evolves
func TestExampleConfigsParse(t *testing.T) {
	for _, path := range []string{"../config/servers.yaml.example"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseConfig(data); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}
