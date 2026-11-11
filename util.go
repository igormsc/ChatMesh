package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

func ensureDir(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("cannot create directory %s: %w", path, err)
	}
	return nil
}

func ensureDirs(paths ...string) error {
	for _, p := range paths {
		if err := ensureDir(p); err != nil {
			return err
		}
	}
	return nil
}

func checkDirStructure() error {
	if err := ensureDirs(sessionDir); err != nil {
		return err
	}
	if _, err := os.Stat("config"); err != nil {
		fmt.Printf("%sNote: config/ not found — create it and add servers.lst to configure servers.%s\n",
			yellow, reset)
	}
	return nil
}

func isValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func generateTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func firstLine(s string, max int) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}
