package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func runWithConfig(cfg *ClientConfig) {
	if cfg.ServerURI != "" && cfg.ServerURI != "select" {
		ollamaHost = strings.TrimRight(cfg.ServerURI, "/")
		fmt.Printf("%s[config]%s server: %s\n", cyan, reset, ollamaHost)
	} else {
		selectServer()
	}

	var model string
	if cfg.Model != "" && cfg.Model != "selected" && cfg.Model != "server" {
		model = cfg.Model
		fmt.Printf("%s[config]%s model: %s\n", cyan, reset, model)
	} else if cfg.Model == "server" {
		model = selectFromServerModels()
	} else {
		model = selectFromConfigModels(true)
		if model == "" {
			model = selectFromServerModels()
		}
	}
	if model == "" {
		fmt.Println(red + "No model selected." + reset)
		os.Exit(1)
	}

	var inboxCh chan Envelope
	if cfg.ListenPort > 0 {
		ch, err := startInboxServer(cfg.ListenPort)
		if err != nil {
			fmt.Printf("%sFailed to start inbox server: %v%s\n", red, err, reset)
			os.Exit(1)
		}
		inboxCh = ch
		fmt.Printf("%s[config]%s listen: 127.0.0.1:%d\n", cyan, reset, cfg.ListenPort)
	}
	if cfg.ForwardURL != "" {
		fmt.Printf("%s[config]%s forward: %s\n", cyan, reset, cfg.ForwardURL)
	}
	if cfg.SendMode == "auto" {
		if cfg.SendCount > 0 {
			fmt.Printf("%s[config]%s send: auto (%d)\n", cyan, reset, cfg.SendCount)
		} else {
			fmt.Printf("%s[config]%s send: auto (unlimited)\n", cyan, reset)
		}
	} else {
		fmt.Printf("%s[config]%s send: manual\n", cyan, reset)
	}

	switch {
	case cfg.Control == "off" || cfg.Control == "":
		fmt.Printf("%s[config]%s control: off\n", cyan, reset)
	default:
		fmt.Printf("%s[config]%s control: %s\n", cyan, reset, cfg.Control)
	}
	if cfg.RouterEnabled {
		fmt.Printf("%s[config]%s router: on (%d peers)\n", cyan, reset, len(cfg.Peers))
	} else {
		fmt.Printf("%s[config]%s router: off\n", cyan, reset)
	}

	switch {
	case cfg.Think == nil:
		fmt.Printf("%s[config]%s think: model default\n", cyan, reset)
	case *cfg.Think:
		fmt.Printf("%s[config]%s think: on\n", cyan, reset)
	default:
		fmt.Printf("%s[config]%s think: off\n", cyan, reset)
	}
	if len(cfg.Options) > 0 {
		keys := make([]string, 0, len(cfg.Options))
		for k := range cfg.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s[config]%s option: %s = %v\n", cyan, reset, k, cfg.Options[k])
		}
	}

	options := make(map[string]interface{})
	for k, v := range cfg.Options {
		options[k] = v
	}
	chatLoop(model, nil, options, cfg, inboxCh)

}

func runInteractive() {
	selectServer()

	cfg := &ClientConfig{
		InstanceName: "local",
		SendMode:     "manual",
		Options:      make(map[string]interface{}),
	}

	if model := selectFromConfigModels(true); model != "" {
		chatLoop(model, nil, nil, cfg, nil)
	}

	for {
		model := selectFromServerModels()
		if model == "" {
			continue
		}
		if strings.ToLower(model) == "q" {
			os.Exit(0)
		}
		chatLoop(model, nil, nil, cfg, nil)
	}
}

func main() {
	var configName string
	flag.StringVar(&configName, "c", "", "configuration section name from config/clients.conf")
	flag.Parse()

	fmt.Printf("%sWelcome to Enhanced Ollama Chat (Go edition)%s\n", bold, reset)

	if err := checkDirStructure(); err != nil {
		fmt.Printf("%sStartup error: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	startStdinReader()

	var cfg *ClientConfig
	if configName != "" {
		c, err := loadClientConfig(configName)
		if err != nil {
			fmt.Printf("%sConfig error: %v%s\n", red, err, reset)
			os.Exit(1)
		}
		cfg = c
		fmt.Printf("%s[config]%s loaded section '%s'\n", cyan, reset, configName)
	} else {
		cfg = loadDefaultClientConfig()
		if cfg != nil {
			fmt.Printf("%s[config]%s loaded section 'default'\n", cyan, reset)
		}
	}

	if cfg != nil {
		runWithConfig(cfg)
		os.Exit(0)
	}
	runInteractive()
}
