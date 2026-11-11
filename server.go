package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Server struct {
	Name string
	URI  string
}

func loadServers() ([]Server, error) {
	configPath := filepath.Join("config", "servers.lst")
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", configPath, err)
	}
	defer f.Close()

	var servers []Server
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			fmt.Printf("%swarning: %s:%d: expected 'name uri', skipping%s\n", yellow, configPath, lineNum, reset)
			continue
		}
		name, uri := fields[0], fields[1]
		if !isValidName(name) {
			fmt.Printf("%swarning: %s:%d: invalid server name '%s', skipping%s\n", yellow, configPath, lineNum, name, reset)
			continue
		}
		u, err := url.Parse(uri)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fmt.Printf("%swarning: %s:%d: invalid uri '%s', skipping%s\n", yellow, configPath, lineNum, uri, reset)
			continue
		}
		servers = append(servers, Server{Name: name, URI: strings.TrimRight(uri, "/")})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return servers, nil
}

func selectServer() {
	servers, err := loadServers()
	if err != nil {
		fmt.Printf("%sError loading servers: %v%s\n", red, err, reset)
		fmt.Println(yellow + "Create config/servers.lst with lines of the form: <name> <uri>" + reset)
		os.Exit(1)
	}
	if len(servers) == 0 {
		fmt.Println(red + "No servers configured in config/servers.lst" + reset)
		os.Exit(1)
	}

	fmt.Printf("\n%sAvailable Servers:%s\n", bold, reset)
	fmt.Printf("%s%4s  %-20s %s%s\n", cyan, "#", "Name", "URI", reset)
	for i, s := range servers {
		fmt.Printf("%s%4d  %-20s %s%s\n", cyan, i+1, s.Name, s.URI, reset)
	}

	for {
		input := readLine("Select server (number): ")
		idx, err := strconv.Atoi(input)
		if err == nil && idx >= 1 && idx <= len(servers) {
			chosen := servers[idx-1]
			ollamaHost = chosen.URI
			fmt.Printf("%sUsing server '%s' (%s)%s\n", green, chosen.Name, chosen.URI, reset)
			return
		}
		fmt.Printf("%sInvalid choice, try again.%s\n", red, reset)
	}
}
