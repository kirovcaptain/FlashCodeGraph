// Package config provides project configuration parsing utilities.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// TsconfigPaths holds the parsed paths configuration from tsconfig.json.
type TsconfigPaths struct {
	BaseUrl string
	Aliases []PathAlias
}

// PathAlias represents a single path alias mapping (e.g., "@models/*" → ["src/models/*"]).
type PathAlias struct {
	Prefix  string   // alias prefix without wildcard (e.g., "@models/")
	Targets []string // target prefixes without wildcard (e.g., ["src/models/"])
}

// ParseTsconfig reads tsconfig.json from the given root directory and extracts paths configuration.
// Returns nil if tsconfig.json doesn't exist or has no paths configured.
func ParseTsconfig(repoRoot string) *TsconfigPaths {
	tsconfigPath := filepath.Join(repoRoot, "tsconfig.json")
	data, err := os.ReadFile(tsconfigPath)
	if err != nil {
		return nil
	}

	var raw struct {
		CompilerOptions struct {
			BaseUrl string            `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if len(raw.CompilerOptions.Paths) == 0 {
		return nil
	}

	baseUrl := raw.CompilerOptions.BaseUrl
	if baseUrl == "" {
		baseUrl = "."
	}

	var aliases []PathAlias
	for pattern, targets := range raw.CompilerOptions.Paths {
		// Strip trailing "/*" from pattern: "@models/*" → "@models/"
		prefix := strings.TrimSuffix(pattern, "*")

		var targetPrefixes []string
		for _, target := range targets {
			// Strip trailing "/*" from target: "src/models/*" → "src/models/"
			targetPrefix := strings.TrimSuffix(target, "*")
			targetPrefixes = append(targetPrefixes, targetPrefix)
		}
		if len(targetPrefixes) > 0 {
			aliases = append(aliases, PathAlias{
				Prefix:  prefix,
				Targets: targetPrefixes,
			})
		}
	}

	if len(aliases) == 0 {
		return nil
	}

	return &TsconfigPaths{
		BaseUrl: baseUrl,
		Aliases: aliases,
	}
}
