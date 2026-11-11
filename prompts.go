package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxPromptSize      = 256 * 1024
	promptPreviewChars = 55
)

type promptFile struct {
	name    string
	path    string
	preview string
	lines   int
}

func listPromptFiles(dir string) ([]promptFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []promptFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			continue
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".swp", ".tmp", ".bak", ".orig":
			continue
		}
		pf, err := inspectPrompt(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		out = append(out, pf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func inspectPrompt(path string) (promptFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return promptFile{}, err
	}
	if info.Size() > maxPromptSize {
		return promptFile{}, fmt.Errorf("too large (%d bytes)", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return promptFile{}, err
	}
	content := string(data)
	lines := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		lines++
	}
	return promptFile{
		name:    filepath.Base(path),
		path:    path,
		preview: firstMeaningfulLine(content),
		lines:   lines,
	}, nil
}

func firstMeaningfulLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line == "" {
			continue
		}
		if len(line) > promptPreviewChars {
			line = line[:promptPreviewChars-1] + "…"
		}
		return line
	}
	return "(empty)"
}

func (p promptFile) read() (string, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func findPromptByName(dir, name string) (promptFile, error) {
	candidates := []string{name}
	if filepath.Ext(name) == "" {
		candidates = append(candidates, name+".txt", name+".md", name+".prompt")
	}
	for _, c := range candidates {
		path := filepath.Join(dir, c)
		if _, err := os.Stat(path); err == nil {
			return inspectPrompt(path)
		}
	}
	return promptFile{}, fmt.Errorf("prompt '%s' not found in %s", name, dir)
}
