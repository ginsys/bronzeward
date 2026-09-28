// Package config reads the deployment settings held outside the database.
package config

import (
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Listen   string   `yaml:"listen"`
	Database Database `yaml:"database"`
}

type Database struct {
	DSN string `yaml:"dsn"`
}

// Load parses exactly one YAML document, refusing unknown fields and missing required ones.
func Load(r io.Reader) (Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config: more than one YAML document")
	}
	if c.Listen == "" {
		return Config{}, errors.New("config: listen is required")
	}
	if c.Database.DSN == "" {
		return Config{}, errors.New("config: database.dsn is required")
	}
	return c, nil
}
