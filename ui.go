package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	italic  = "\033[3m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	grey    = "\033[90m"
)

var (
	stdin   = bufio.NewReader(os.Stdin)
	stdinCh = make(chan string, 8)
)

func startStdinReader() {
	go func() {
		for {
			line, err := stdin.ReadString('\n')
			line = strings.TrimSpace(line)
			if err != nil {
				if line != "" {
					stdinCh <- line
				}
				close(stdinCh)
				return
			}
			stdinCh <- line
		}
	}()
}

func readLine(prompt string) string {
	fmt.Print(prompt)
	line, ok := <-stdinCh
	if !ok {
		fmt.Println(bold + "\nGoodbye!" + reset)
		os.Exit(0)
	}
	return line
}

func readMultiLine() string {
	fmt.Println(dim + "Multi-line input (end with a line containing 'EOF'):" + reset)
	var lines []string
	for {
		line, ok := <-stdinCh
		if !ok {
			fmt.Println(yellow + "\nMulti-line input cancelled." + reset)
			return ""
		}
		if line == "EOF" {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func chooseFromList(items []string, title string) (string, error) {
	fmt.Printf("%s%s%s\n", bold, title, reset)
	for i, item := range items {
		fmt.Printf("%s%2d%s %s\n", cyan, i+1, reset, item)
	}
	input := readLine("Enter number or press Enter to skip: ")
	if input == "" {
		return "", fmt.Errorf("cancelled")
	}
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(items) {
		return "", fmt.Errorf("invalid choice")
	}
	return items[idx-1], nil
}

func fmtSize(size int64) string {
	if size <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f GB", float64(size)/1e9)
}

func fmtTime(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Format("2006-01-02 15:04")
}

func displayModels(models []OllamaModel) {
	if len(models) == 0 {
		fmt.Println(yellow + "No models found locally." + reset)
		return
	}
	fmt.Printf("\n%sAvailable Models:%s\n", bold, reset)
	fmt.Printf("%s%4s  %-30s %-10s %s%s\n", cyan, "#", "Name", "Size", "Modified", reset)
	for i, m := range models {
		fmt.Printf("%s%4d  %-30s %-10s %s%s\n",
			cyan, i+1, m.Name, fmtSize(m.Size), fmtTime(m.ModifiedAt), reset)
	}
}

func readConfigModels() []string {
	configPath := filepath.Join("config", "models.lst")
	f, err := os.Open(configPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var models []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		models = append(models, line)
	}
	return models
}

func filterAvailableModels(configured []string, available []OllamaModel) (present, missing []string) {
	availableSet := make(map[string]bool, len(available))
	for _, m := range available {
		availableSet[m.Name] = true
	}
	for _, c := range configured {
		if availableSet[c] {
			present = append(present, c)
		} else {
			missing = append(missing, c)
		}
	}
	return present, missing
}

func selectFromConfigModels(filterByServer bool) string {
	configModels := readConfigModels()
	if len(configModels) == 0 {
		return ""
	}
	if filterByServer {
		available, err := fetchModels()
		if err != nil {
			fmt.Printf("%sCould not reach server to verify configured models: %v%s\n",
				yellow, err, reset)
		} else {
			present, missing := filterAvailableModels(configModels, available)
			if len(missing) > 0 {
				fmt.Printf("%sNot available on this server: %s%s\n",
					yellow, strings.Join(missing, ", "), reset)
			}
			configModels = present
		}
	}
	if len(configModels) == 0 {
		return ""
	}
	fmt.Printf("\n%sConfigured Models:%s\n", bold, reset)
	for i, m := range configModels {
		fmt.Printf("%s%2d%s %s\n", cyan, i+1, reset, m)
	}
	input := readLine("Enter model index, or press Enter to list all installed models: ")
	if input == "" {
		return ""
	}
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(configModels) {
		return ""
	}
	return configModels[idx-1]
}

func selectFromServerModels() string {
	models, err := fetchModels()
	if err != nil {
		fmt.Printf("%sError fetching models: %v%s\n", red, err, reset)
		return readLine("Enter model name to use: ")
	}
	if len(models) == 0 {
		fmt.Println(yellow + "No models found locally. Please enter a model name." + reset)
		return readLine("Model name: ")
	}
	displayModels(models)
	input := readLine("Enter model index or name: ")
	if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1].Name
	}
	return input
}
