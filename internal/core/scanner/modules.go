package scanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// detectMavenModules reads pom.xml for <modules> entries.
func (scanner *Scanner) detectMavenModules(root string, info *ProjectInfo) {
	pomPath := filepath.Join(root, "pom.xml")
	data, err := os.ReadFile(pomPath)
	if err != nil {
		return
	}
	content := string(data)

	// Simple extraction: find <module>xxx</module> entries
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<module>") && strings.HasSuffix(line, "</module>") {
			moduleName := strings.TrimPrefix(line, "<module>")
			moduleName = strings.TrimSuffix(moduleName, "</module>")
			moduleName = strings.TrimSpace(moduleName)
			if moduleName != "" {
				moduleRoot := filepath.Join(root, moduleName)
				sourceDirectory := filepath.Join(moduleName, "src", "main", "java")
				info.SubModules = append(info.SubModules, SubModule{
					Name:    moduleName,
					RootDir: moduleRoot,
					SrcDir:  sourceDirectory,
				})
				info.SourceDirs[moduleName] = sourceDirectory
				// Add sub-module pom.xml to BuildFiles for framework detection
				subPom := filepath.Join(moduleName, "pom.xml")
				if _, err := os.Stat(filepath.Join(root, subPom)); err == nil {
					info.BuildFiles = append(info.BuildFiles, subPom)
				}
			}
		}
	}

	// Default source directory for single-module Maven project
	if len(info.SubModules) == 0 {
		info.SourceDirs["default"] = "src/main/java"
	}
}

// detectGradleModules reads settings.gradle for include entries.
func (scanner *Scanner) detectGradleModules(root string, info *ProjectInfo) {
	settingsFiles := []string{"settings.gradle", "settings.gradle.kts"}
	for _, settingsFile := range settingsFiles {
		data, err := os.ReadFile(filepath.Join(root, settingsFile))
		if err != nil {
			continue
		}
		content := string(data)
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if !strings.Contains(line, "include") {
				continue
			}
			// Extract quoted module names: include ':api', ':core'
			for _, part := range strings.Split(line, ",") {
				part = strings.TrimSpace(part)
				// Remove include keyword, quotes, colons
				part = strings.TrimPrefix(part, "include")
				part = strings.TrimPrefix(part, "(")
				part = strings.TrimSuffix(part, ")")
				part = strings.Trim(part, " '\":")
				if part != "" && !strings.ContainsAny(part, "{}()") {
					moduleRoot := filepath.Join(root, part)
					sourceDirectory := filepath.Join(part, "src", "main", "java")
					info.SubModules = append(info.SubModules, SubModule{
						Name:    part,
						RootDir: moduleRoot,
						SrcDir:  sourceDirectory,
					})
					info.SourceDirs[part] = sourceDirectory
					// Add sub-module build file for framework detection
					for _, bf := range []string{"build.gradle", "build.gradle.kts"} {
						subBuild := filepath.Join(part, bf)
						if _, err := os.Stat(filepath.Join(root, subBuild)); err == nil {
							info.BuildFiles = append(info.BuildFiles, subBuild)
							break
						}
					}
				}
			}
		}
	}

	if len(info.SubModules) == 0 {
		info.SourceDirs["default"] = "src/main/java"
	}
}

// detectNpmWorkspaces reads package.json for workspaces field.
func (scanner *Scanner) detectNpmWorkspaces(root string, info *ProjectInfo) {
	packagePath := filepath.Join(root, "package.json")
	data, err := os.ReadFile(packagePath)
	if err != nil {
		return
	}

	var packageJSON struct {
		Workspaces interface{} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &packageJSON); err != nil {
		return
	}

	var patterns []string
	switch workspaces := packageJSON.Workspaces.(type) {
	case []interface{}:
		for _, workspace := range workspaces {
			if pattern, ok := workspace.(string); ok {
				patterns = append(patterns, pattern)
			}
		}
	case map[string]interface{}:
		if packages, ok := workspaces["packages"].([]interface{}); ok {
			for _, pkg := range packages {
				if pattern, ok := pkg.(string); ok {
					patterns = append(patterns, pattern)
				}
			}
		}
	}

	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, match := range matches {
			matchInfo, err := os.Stat(match)
			if err != nil || !matchInfo.IsDir() {
				continue
			}
			moduleName := filepath.Base(match)
			relPath, _ := filepath.Rel(root, match)
			info.SubModules = append(info.SubModules, SubModule{
				Name:    moduleName,
				RootDir: match,
				SrcDir:  relPath,
			})
			info.SourceDirs[moduleName] = relPath
			// Add sub-module package.json for framework detection
			subPkg := filepath.Join(relPath, "package.json")
			if _, err := os.Stat(filepath.Join(root, subPkg)); err == nil {
				info.BuildFiles = append(info.BuildFiles, subPkg)
			}
		}
	}
}

// detectGoModules detects Go workspace modules (go.work).
func (scanner *Scanner) detectGoModules(root string, info *ProjectInfo) {
	workPath := filepath.Join(root, "go.work")
	data, err := os.ReadFile(workPath)
	if err != nil {
		// Single module project
		info.SourceDirs["default"] = "."
		return
	}

	content := string(data)
	inUseBlock := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "use (" {
			inUseBlock = true
			continue
		}
		if line == ")" {
			inUseBlock = false
			continue
		}
		if inUseBlock && line != "" {
			modulePath := strings.Trim(line, " \t\"")
			if modulePath != "" && modulePath != "." {
				info.SubModules = append(info.SubModules, SubModule{
					Name:    filepath.Base(modulePath),
					RootDir: filepath.Join(root, modulePath),
					SrcDir:  modulePath,
				})
				info.SourceDirs[filepath.Base(modulePath)] = modulePath
			}
		}
	}

	if len(info.SubModules) == 0 {
		info.SourceDirs["default"] = "."
	}
}
