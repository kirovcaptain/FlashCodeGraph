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
		if _, err := os.Stat(filepath.Join(root, "src", "main", "kotlin")); err == nil {
			info.SourceDirs["default-kotlin"] = "src/main/kotlin"
		}
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
				part = strings.ReplaceAll(part, ":", string(filepath.Separator))
				if part != "" && !strings.ContainsAny(part, "{}()") {
					moduleRoot := filepath.Join(root, part)
					sourceDirectory := filepath.Join(part, "src", "main", "java")
					info.SubModules = append(info.SubModules, SubModule{
						Name:    part,
						RootDir: moduleRoot,
						SrcDir:  sourceDirectory,
					})
					info.SourceDirs[part] = sourceDirectory
					// Also register src/main/kotlin if it exists
					kotlinSourceDirectory := filepath.Join(part, "src", "main", "kotlin")
					if _, err := os.Stat(filepath.Join(root, kotlinSourceDirectory)); err == nil {
						info.SourceDirs[part+"-kotlin"] = kotlinSourceDirectory
					}
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
		if _, err := os.Stat(filepath.Join(root, "src", "main", "kotlin")); err == nil {
			info.SourceDirs["default-kotlin"] = "src/main/kotlin"
		}
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

// detectGoModules detects Go workspace modules from go.work, or falls back to scanning subdirectories for go.mod.
func (scanner *Scanner) detectGoModules(root string, info *ProjectInfo) {
	workFilePath := filepath.Join(root, "go.work")
	data, err := os.ReadFile(workFilePath)
	if err != nil {
		// No go.work — fallback to scanning subdirectories for multiple go.mod files
		scanner.detectGoModulesByScanning(root, info)
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
		// Block form: lines inside use ( ... )
		if inUseBlock && line != "" {
			modulePath := strings.Trim(line, " \t\"")
			scanner.addGoSubModule(root, modulePath, info)
			continue
		}
		// Single-line form: use ./service-a
		if strings.HasPrefix(line, "use ") {
			modulePath := strings.TrimSpace(strings.TrimPrefix(line, "use"))
			modulePath = strings.Trim(modulePath, "\"")
			scanner.addGoSubModule(root, modulePath, info)
		}
	}

	if len(info.SubModules) == 0 {
		info.SourceDirs["default"] = "."
	}
}

// addGoSubModule registers a Go sub-module path into ProjectInfo.
func (scanner *Scanner) addGoSubModule(root, modulePath string, info *ProjectInfo) {
	if modulePath == "" || modulePath == "." {
		return
	}
	info.SubModules = append(info.SubModules, SubModule{
		Name:    filepath.Base(modulePath),
		RootDir: filepath.Join(root, modulePath),
		SrcDir:  modulePath,
	})
	info.SourceDirs[filepath.Base(modulePath)] = modulePath
}

// detectGoModulesByScanning scans immediate subdirectories for go.mod files when no go.work exists.
func (scanner *Scanner) detectGoModulesByScanning(root string, info *ProjectInfo) {
	entries, err := os.ReadDir(root)
	if err != nil {
		info.SourceDirs["default"] = "."
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		goModPath := filepath.Join(root, entry.Name(), "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			info.SubModules = append(info.SubModules, SubModule{
				Name:    entry.Name(),
				RootDir: filepath.Join(root, entry.Name()),
				SrcDir:  entry.Name(),
			})
			info.SourceDirs[entry.Name()] = entry.Name()
		}
	}
	if len(info.SubModules) == 0 {
		info.SourceDirs["default"] = "."
	}
}
