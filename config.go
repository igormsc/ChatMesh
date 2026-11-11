package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var validOptions = map[string]string{
	"temperature":       "float (0.0-2.0) - creativity",
	"num_ctx":           "int - context window size",
	"num_predict":       "int - max tokens to generate",
	"top_k":             "int - limit to top K tokens",
	"top_p":             "float - nucleus sampling probability",
	"min_p":             "float - minimum probability threshold",
	"tfs_z":             "float - tail free sampling",
	"repeat_penalty":    "float (>=1.0) - repetition penalty",
	"repeat_last_n":     "int - tokens to look back for penalty",
	"seed":              "int - random seed",
	"stop":              "string/array - stop sequence(s)",
	"mirostat":          "int (0,1,2) - mirostat sampling",
	"mirostat_eta":      "float - mirostat learning rate",
	"mirostat_tau":      "float - mirostat balance",
	"presence_penalty":  "float - new token penalty",
	"frequency_penalty": "float - frequency penalty",
}

type ClientConfig struct {
	InstanceName  string
	ServerURI     string
	Model         string
	Think         *bool
	Options       map[string]interface{}
	ListenPort    int
	ForwardURL    string
	SendMode      string
	SendCount     int
	PromptPath    string
	Control       string // "off" | "on" | "auto" | "auto N"
	RouterEnabled bool
	Peers         map[string]string // alias → inbox URL
}

type rawConfigSection struct {
	name string
	keys map[string]string
}

func parseConfigFile(path string) ([]rawConfigSection, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sections []rawConfigSection
	var current *rawConfigSection
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if !isValidName(name) {
				fmt.Printf("%swarning: %s:%d: invalid section name '%s', skipping%s\n",
					yellow, path, lineNum, name, reset)
				current = nil
				continue
			}
			sections = append(sections, rawConfigSection{name: name, keys: make(map[string]string)})
			current = &sections[len(sections)-1]
			continue
		}
		if current == nil {
			fmt.Printf("%swarning: %s:%d: key-value outside section, skipping%s\n", yellow, path, lineNum, reset)
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			fmt.Printf("%swarning: %s:%d: expected 'key = value', skipping%s\n", yellow, path, lineNum, reset)
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		current.keys[key] = value
	}
	return sections, scanner.Err()
}

func parseConfigOption(s string) interface{} {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		var v interface{}
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			return v
		}
	}
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func buildClientConfig(raw *rawConfigSection) (*ClientConfig, error) {
	cfg := &ClientConfig{
		InstanceName: raw.name,
		ServerURI:    "select",
		Model:        "selected",
		Options:      make(map[string]interface{}),
		SendMode:     "manual",
		SendCount:    0,
		Control:      "off",
		Peers:        make(map[string]string),
	}
	for k, v := range raw.keys {
		switch k {
		case "server":
			cfg.ServerURI = v
		case "model":
			cfg.Model = v
		case "think":
			var on bool
			switch strings.ToLower(v) {
			case "on", "true", "1", "yes":
				on = true
				cfg.Think = &on
			case "off", "false", "0", "no":
				on = false
				cfg.Think = &on
			default:
				return nil, fmt.Errorf("invalid think value '%s'", v)
			}
		case "listen_port":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("invalid listen_port '%s'", v)
			}
			cfg.ListenPort = n
		case "forward_url":
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, fmt.Errorf("invalid forward_url '%s'", v)
			}
			cfg.ForwardURL = v
		case "send_mode":
			switch strings.ToLower(v) {
			case "manual", "auto":
				cfg.SendMode = strings.ToLower(v)
			default:
				return nil, fmt.Errorf("invalid send_mode '%s' (use manual|auto)", v)
			}
		case "send_count":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid send_count '%s'", v)
			}
			cfg.SendCount = n
		case "instance_name":
			if !isValidName(v) {
				return nil, fmt.Errorf("invalid instance_name '%s'", v)
			}
			cfg.InstanceName = v
		case "prompt_path":
			cfg.PromptPath = v
		case "control":
			v = strings.ToLower(strings.TrimSpace(v))
			switch {
			case v == "off":
				cfg.Control = "off"
			case v == "on" || v == "manual":
				cfg.Control = "on"
			case v == "auto":
				cfg.Control = "auto"
			case strings.HasPrefix(v, "auto "):
				n, err := strconv.Atoi(strings.TrimSpace(v[5:]))
				if err != nil || n < 0 {
					return nil, fmt.Errorf("invalid control value '%s' (use off|on|auto|auto N)", v)
				}
				if n == 0 {
					cfg.Control = "auto"
				} else {
					cfg.Control = fmt.Sprintf("auto %d", n)
				}
			default:
				return nil, fmt.Errorf("invalid control value '%s' (use off|on|auto|auto N)", v)
			}
		case "router":
			switch strings.ToLower(v) {
			case "on", "true", "1", "yes":
				cfg.RouterEnabled = true
			case "off", "false", "0", "no":
				cfg.RouterEnabled = false
			default:
				return nil, fmt.Errorf("invalid router value '%s' (use on|off)", v)
			}
		case "peers":
			for _, pair := range strings.Split(v, ",") {
				pair = strings.TrimSpace(pair)
				if pair == "" {
					continue
				}
				idx := strings.Index(pair, ":")
				if idx < 0 {
					return nil, fmt.Errorf("invalid peer entry '%s' (expected alias:url)", pair)
				}
				alias := strings.TrimSpace(pair[:idx])
				urlStr := strings.TrimSpace(pair[idx+1:])
				if !isValidName(alias) {
					return nil, fmt.Errorf("invalid peer alias '%s'", alias)
				}
				u, err := url.Parse(urlStr)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return nil, fmt.Errorf("invalid peer url '%s'", urlStr)
				}
				cfg.Peers[alias] = urlStr
			}
		default:
			if _, known := validOptions[k]; !known {
				fmt.Printf("%swarning: config [%s]: unknown option '%s', ignoring%s\n",
					yellow, raw.name, k, reset)
				continue
			}
			cfg.Options[k] = parseConfigOption(v)
		}
	}
	if cfg.ServerURI != "select" {
		u, err := url.Parse(cfg.ServerURI)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("invalid server '%s' (use 'select' or a full http(s) URL)", cfg.ServerURI)
		}
	}
	return cfg, nil
}

func loadClientConfig(name string) (*ClientConfig, error) {
	configPath := filepath.Join("config", "clients.conf")
	sections, err := parseConfigFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", configPath, err)
	}
	for i := range sections {
		if sections[i].name == name {
			return buildClientConfig(&sections[i])
		}
	}
	return nil, fmt.Errorf("section '%s' not found in %s", name, configPath)
}

func loadDefaultClientConfig() *ClientConfig {
	configPath := filepath.Join("config", "clients.conf")
	sections, err := parseConfigFile(configPath)
	if err != nil {
		return nil
	}
	for i := range sections {
		if sections[i].name == "default" {
			cfg, err := buildClientConfig(&sections[i])
			if err != nil {
				fmt.Printf("%swarning: default config: %v%s\n", yellow, err, reset)
				return nil
			}
			return cfg
		}
	}
	return nil
}
