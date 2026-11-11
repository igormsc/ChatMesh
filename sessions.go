package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var sessionDir = filepath.Join(".", "log_ollama_chat_session")

type SessionFile struct {
	Messages []Message              `json:"messages"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

func listSessionFiles() []string {
	files, _ := os.ReadDir(sessionDir)
	var jsons []string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			info, err := f.Info()
			if err == nil {
				jsons = append(jsons, info.ModTime().Format(time.RFC3339)+":::"+f.Name())
			}
		}
	}
	sort.Slice(jsons, func(i, j int) bool {
		return jsons[i] > jsons[j]
	})
	var names []string
	for _, s := range jsons {
		parts := strings.SplitN(s, ":::", 2)
		if len(parts) == 2 {
			names = append(names, parts[1])
		}
	}
	return names
}

func saveSession(filename string, messages []Message, options map[string]interface{}) {
	if !strings.HasSuffix(filename, ".json") {
		filename += ".json"
	}
	if err := ensureDir(sessionDir); err != nil {
		fmt.Printf("%sError saving session: %v%s\n", red, err, reset)
		return
	}
	path := filepath.Join(sessionDir, filename)

	session := SessionFile{Messages: messages, Options: options}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		fmt.Printf("%sError saving session: %v%s\n", red, err, reset)
		return
	}

	tmp, err := os.CreateTemp(sessionDir, ".tmp_"+filename+"_*")
	if err != nil {
		fmt.Printf("%sError saving session: %v%s\n", red, err, reset)
		return
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		fmt.Printf("%sError saving session: %v%s\n", red, err, reset)
		return
	}
	if err := tmp.Close(); err != nil {
		fmt.Printf("%sError saving session: %v%s\n", red, err, reset)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		fmt.Printf("%sError saving session: %v%s\n", red, err, reset)
		return
	}
	success = true
	fmt.Printf("%sSession saved to %s%s\n", green, path, reset)
}

func loadSession(filename string) ([]Message, map[string]interface{}, error) {
	path := filepath.Join(sessionDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var session SessionFile
	if err := json.Unmarshal(data, &session); err == nil && len(session.Messages) > 0 {
		for _, m := range session.Messages {
			if m.Role == "" {
				return nil, nil, fmt.Errorf("invalid message found")
			}
		}
		return session.Messages, session.Options, nil
	}
	var messages []Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, nil, fmt.Errorf("invalid session format")
	}
	for _, m := range messages {
		if m.Role == "" {
			return nil, nil, fmt.Errorf("invalid message found")
		}
	}
	return messages, nil, nil
}

func getSessionPreview(filename string) string {
	messages, _, err := loadSession(filename)
	if err != nil {
		return dim + "(unreadable)" + reset
	}
	for _, m := range messages {
		if m.Role == "user" && m.Content != "" {
			snippet := m.Content
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			return grey + dim + snippet + reset
		}
	}
	return dim + "(empty)" + reset
}
