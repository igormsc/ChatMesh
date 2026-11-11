package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type cmdResult struct {
	replacedInput string
	stopLoop      bool
}

type command func(s *chatSession, args string) cmdResult

var commands = map[string]command{
	"bye":     cmdBye,
	"exit":    cmdBye,
	"quit":    cmdBye,
	"stop":    cmdStop,
	"list":    cmdList,
	"models":  cmdList,
	"model":   cmdModel,
	"save":    cmdSave,
	"load":    cmdLoad,
	"clear":   cmdClear,
	"image":   cmdImage,
	"m":       cmdMultiLine,
	"set":     cmdSet,
	"think":   cmdThink,
	"info":    cmdInfo,
	"show":    cmdShow,
	"send":    cmdSend,
	"prompt":  cmdPrompt,
	"restore": cmdRestore,
	"control": cmdControl,
	"router":  cmdRouter,
	"exec":    cmdExec,
	"del":     cmdDel,
}

func cmdBye(s *chatSession, args string) cmdResult {
	s.saveTimestamped("auto")
	fmt.Println(bold + "Goodbye!" + reset)
	return cmdResult{stopLoop: true}
}

func cmdStop(s *chatSession, args string) cmdResult {
	m := s.getModel()
	if err := stopModel(m); err != nil {
		fmt.Printf("%sCould not stop model: %v%s\n", red, err, reset)
	} else {
		fmt.Printf("%sModel '%s' unloaded.%s\n", yellow, m, reset)
	}
	s.saveTimestamped("auto")
	return cmdResult{stopLoop: true}
}

func cmdMultiLine(s *chatSession, args string) cmdResult {
	multi := readMultiLine()
	if multi == "" {
		return cmdResult{}
	}
	return cmdResult{replacedInput: multi}
}

func cmdSet(s *chatSession, args string) cmdResult {
	if args == "" {
		_, opts, _ := s.snapshot()
		if len(opts) == 0 {
			fmt.Println(dim + "No custom options set (using model defaults)." + reset)
		} else {
			fmt.Printf("%sCurrent model options:%s\n", bold, reset)
			for k, v := range opts {
				fmt.Printf("  %s%s%s = %v\n", cyan, k, reset, v)
			}
		}
		return cmdResult{}
	}
	kvParts := strings.SplitN(args, " ", 2)
	key := kvParts[0]
	if len(kvParts) >= 2 && kvParts[1] != "" {
		if _, known := validOptions[key]; !known {
			fmt.Printf("%sUnknown parameter '%s'. Valid parameters:%s\n", red, key, reset)
			for k, desc := range validOptions {
				fmt.Printf("  %s%s%s - %s\n", cyan, k, reset, desc)
			}
			return cmdResult{}
		}
	}
	if len(kvParts) < 2 || kvParts[1] == "" {
		s.mu.Lock()
		delete(s.currentOptions, key)
		s.mu.Unlock()
		fmt.Printf("%sRemoved option '%s'%s\n", yellow, key, reset)
		return cmdResult{}
	}
	valueStr := strings.TrimSpace(kvParts[1])
	var value interface{}
	if strings.HasPrefix(valueStr, "[") {
		var arr []interface{}
		if err := json.Unmarshal([]byte(valueStr), &arr); err != nil {
			fmt.Printf("%sInvalid JSON array: %v%s\n", red, err, reset)
			return cmdResult{}
		}
		value = arr
	} else if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
		if f == float64(int64(f)) && !strings.Contains(valueStr, ".") {
			value = int64(f)
		} else {
			value = f
		}
	} else if b, err := strconv.ParseBool(valueStr); err == nil {
		value = b
	} else {
		value = valueStr
	}
	s.mu.Lock()
	s.currentOptions[key] = value
	s.mu.Unlock()
	fmt.Printf("%sSet %s = %v%s\n", green, key, value, reset)
	return cmdResult{}
}

func cmdSend(s *chatSession, args string) cmdResult {
	if s.cfg.ForwardURL == "" {
		fmt.Println(red + "No forward_url configured for this client." + reset)
		return cmdResult{}
	}
	if args == "" {
		s.mu.Lock()
		s.sendState.mode = "manual"
		s.sendState.remaining = 0
		s.currentTrace = generateTraceID()
		s.mu.Unlock()
		s.fireLatest()
		fmt.Printf("%sSend mode: manual%s\n", yellow, reset)
		return cmdResult{}
	}

	sendParts := strings.Fields(args)
	if sendParts[0] != "auto" {
		fmt.Println(red + "Usage: /send | /send auto [N]" + reset)
		return cmdResult{}
	}
	s.mu.Lock()
	s.sendState.mode = "auto"
	if len(sendParts) >= 2 {
		n, err := strconv.Atoi(sendParts[1])
		if err != nil || n < 0 {
			s.mu.Unlock()
			fmt.Printf("%sInvalid count: %s%s\n", red, sendParts[1], reset)
			return cmdResult{}
		}
		if n == 0 {
			s.sendState.remaining = -1
		} else {
			s.sendState.remaining = n
		}
	} else {
		s.sendState.remaining = -1
	}
	remaining := s.sendState.remaining
	s.mu.Unlock()
	if remaining == -1 {
		fmt.Printf("%sSend mode: auto (unlimited)%s\n", green, reset)
	} else {
		fmt.Printf("%sSend mode: auto (%d remaining)%s\n", green, remaining, reset)
	}
	return cmdResult{}
}

func cmdShow(s *chatSession, args string) cmdResult {
	m := s.getModel()
	info, err := fetchModelInfo(m)
	if err != nil {
		fmt.Printf("%sError fetching model info: %v%s\n", red, err, reset)
		return cmdResult{}
	}
	fmt.Printf("\n%s%s capabilities:%s\n", bold, m, reset)
	if info.Details.Family != "" {
		fmt.Printf("  Family: %s\n", info.Details.Family)
	}
	if info.Details.ParameterSize != "" {
		fmt.Printf("  Size: %s\n", info.Details.ParameterSize)
	}
	if info.Details.QuantizationLevel != "" {
		fmt.Printf("  Quantization: %s\n", info.Details.QuantizationLevel)
	}
	if info.ModelInfo == nil {
		fmt.Println(dim + "  No model_info available." + reset)
		return cmdResult{}
	}
	found := false
	for _, sf := range showFields {
		matchedKey := ""
		for k := range info.ModelInfo {
			if strings.HasSuffix(k, "."+sf.Suffix) || k == sf.Suffix {
				matchedKey = k
				break
			}
		}
		if matchedKey != "" {
			val := info.ModelInfo[matchedKey]
			if val == nil {
				val = "N/A"
			}
			fmt.Printf("  %s%s%s: %v\n", cyan, sf.Label, reset, val)
			found = true
		}
	}
	if !found {
		fmt.Println(dim + "  No recognized capability keys found." + reset)
	}
	return cmdResult{}
}

func cmdThink(s *chatSession, args string) cmdResult {
	if args == "" {
		if s.thinkSetting == nil {
			fmt.Println(dim + "Thinking: model default" + reset)
		} else if *s.thinkSetting {
			fmt.Printf("%sThinking: ON%s\n", green, reset)
		} else {
			fmt.Printf("%sThinking: OFF%s\n", yellow, reset)
		}
		return cmdResult{}
	}
	val := strings.ToLower(strings.TrimSpace(args))
	var on bool
	switch val {
	case "on", "true", "1", "yes":
		on = true
		s.thinkSetting = &on
	case "off", "false", "0", "no":
		on = false
		s.thinkSetting = &on
	default:
		fmt.Printf("%sInvalid value. Use on/off or true/false.%s\n", red, reset)
		return cmdResult{}
	}
	if *s.thinkSetting {
		fmt.Printf("%sThinking: ON%s\n", green, reset)
	} else {
		fmt.Printf("%sThinking: OFF%s\n", yellow, reset)
	}
	return cmdResult{}
}

func cmdList(s *chatSession, args string) cmdResult {
	models, err := fetchModels()
	if err != nil {
		fmt.Printf("%sError: %v%s\n", red, err, reset)
	} else {
		displayModels(models)
	}
	return cmdResult{}
}

func cmdInfo(s *chatSession, args string) cmdResult {
	m := s.getModel()
	info, err := fetchModelInfo(m)
	if err != nil {
		fmt.Printf("%sError fetching model info: %v%s\n", red, err, reset)
		return cmdResult{}
	}
	fmt.Printf("\n%sModel: %s%s\n", bold, m, reset)
	if info.Details.Family != "" {
		fmt.Printf("  Family: %s\n", info.Details.Family)
	}
	if info.Details.ParameterSize != "" {
		fmt.Printf("  Size: %s\n", info.Details.ParameterSize)
	}
	if info.Details.QuantizationLevel != "" {
		fmt.Printf("  Quantization: %s\n", info.Details.QuantizationLevel)
	}
	var paramsMap map[string]interface{}
	if err := json.Unmarshal(info.Parameters, &paramsMap); err != nil {
		var paramStr string
		if err2 := json.Unmarshal(info.Parameters, &paramStr); err2 == nil {
			fmt.Printf("\n%sParameters (raw):%s\n%s\n", bold, reset, paramStr)
		} else {
			fmt.Printf("\n%sParameters (raw):%s\n%s\n", bold, reset, string(info.Parameters))
		}
	} else if len(paramsMap) > 0 {
		fmt.Printf("\n%sDefault parameters:%s\n", bold, reset)
		keys := make([]string, 0, len(paramsMap))
		for k := range paramsMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_, optsSnap, _ := s.snapshot()
		for _, k := range keys {
			v := paramsMap[k]
			if userVal, overridden := optsSnap[k]; overridden {
				fmt.Printf("  %s%s%s = %v %s(user: %v)%s\n", cyan, k, reset, v, yellow, userVal, reset)
			} else {
				fmt.Printf("  %s%s%s = %v\n", cyan, k, reset, v)
			}
		}
	} else {
		fmt.Printf("\n%sDefault parameters: (none)%s\n", dim, reset)
	}
	if s.thinkSetting != nil {
		if *s.thinkSetting {
			fmt.Printf("\n%sThinking: ON (forced)%s\n", green, reset)
		} else {
			fmt.Printf("\n%sThinking: OFF (forced)%s\n", yellow, reset)
		}
	}
	return cmdResult{}
}

func cmdModel(s *chatSession, args string) cmdResult {
	if args == "" {
		fmt.Println(red + "Usage: /model <model_name>" + reset)
		return cmdResult{}
	}
	s.setModel(args)
	fmt.Printf("%sNow using %s. (conversation preserved)%s\n", green, args, reset)
	return cmdResult{}
}

func cmdSave(s *chatSession, args string) cmdResult {
	if args == "" {
		s.saveTimestamped("session")
		return cmdResult{}
	}
	msgs, opts, _ := s.snapshot()
	saveSession(args, msgs, opts)
	return cmdResult{}
}

func cmdLoad(s *chatSession, args string) cmdResult {
	var file string
	if args == "" {
		files := listSessionFiles()
		if len(files) == 0 {
			fmt.Println(yellow + "No saved sessions found." + reset)
			return cmdResult{}
		}
		fmt.Printf("%sSaved Sessions:%s\n", bold, reset)
		for i, f := range files {
			info, err := os.Stat(filepath.Join(sessionDir, f))
			dateStr := ""
			if err == nil {
				dateStr = info.ModTime().Format("2006-01-02 15:04")
			}
			fmt.Printf("%s%2d%s %s %s\n", cyan, i+1, reset, f, dateStr)
		}
		choice, err := chooseFromList(files, "Select session to load")
		if err != nil {
			return cmdResult{}
		}
		file = choice
	} else {
		file = args
	}
	_, count, err := loadSessionInto(s, file)
	if err != nil {
		fmt.Printf("%sError loading: %v%s\n", red, err, reset)
		return cmdResult{}
	}
	fmt.Printf("%sLoaded %d messages from %s%s\n", green, count, file, reset)
	return cmdResult{}
}

func cmdRestore(s *chatSession, args string) cmdResult {
	var file string
	if args == "" {
		files := listSessionFiles()
		if len(files) == 0 {
			fmt.Println(yellow + "No saved sessions found." + reset)
			return cmdResult{}
		}
		m := s.getModel()
		safeName := strings.ReplaceAll(m, "/", "_")
		var filtered []string
		for _, f := range files {
			if strings.Contains(f, safeName) {
				filtered = append(filtered, f)
			}
		}
		if len(filtered) == 0 {
			fmt.Printf("%sNo saved sessions found for model %s.%s\n", yellow, m, reset)
			fmt.Println(dim + "Use /load to load any session regardless of model." + reset)
			return cmdResult{}
		}
		displayLimit := 10
		if len(filtered) < displayLimit {
			displayLimit = len(filtered)
		}
		fmt.Printf("\n%sRecent sessions for %s:%s\n", bold, m, reset)
		for i := 0; i < displayLimit; i++ {
			preview := getSessionPreview(filtered[i])
			fmt.Printf("%s%3d%s  %-40s %s\n", cyan, i+1, reset, filtered[i], preview)
		}
		input := readLine("Select session (Enter to cancel): ")
		if input == "" {
			return cmdResult{}
		}
		idx, err := strconv.Atoi(input)
		if err != nil || idx < 1 || idx > displayLimit {
			fmt.Printf("%sInvalid choice.%s\n", red, reset)
			return cmdResult{}
		}
		file = filtered[idx-1]
	} else {
		file = args
	}

	last, count, err := loadSessionInto(s, file)
	if err != nil {
		fmt.Printf("%sError loading: %v%s\n", red, err, reset)
		return cmdResult{}
	}
	fmt.Printf("%sRestored %d messages from %s%s\n", green, count, file, reset)
	showLastMessage(last)
	return cmdResult{}
}

func showLastMessage(m Message) {
	if m.Role == "" {
		return
	}
	fmt.Printf("\n%sLast message of restored session:%s\n", bold+cyan, reset)
	roleLabel := strings.ToUpper(m.Role[:1]) + m.Role[1:]
	fmt.Printf("%s%s%s\n", bold, roleLabel, reset)
	if m.Reasoning != "" {
		fmt.Printf("%s%s%s%s\n", grey, dim+italic, m.Reasoning, reset)
	}
	if m.Content != "" {
		if m.Role == "assistant" {
			fmt.Println(blue + m.Content + reset)
		} else {
			fmt.Println(m.Content)
		}
	}
	fmt.Println()
}

func cmdClear(s *chatSession, args string) cmdResult {
	s.mu.Lock()
	s.messages = nil
	s.mu.Unlock()
	hadPrefix := s.pendingPrefix != ""
	s.pendingPrefix = ""
	if hadPrefix {
		fmt.Println(yellow + "Conversation cleared (pending prompt also discarded)." + reset)
	} else {
		fmt.Println(yellow + "Conversation cleared." + reset)
	}
	return cmdResult{}
}

func cmdImage(s *chatSession, args string) cmdResult {
	if args == "" {
		fmt.Println(red + "Usage: /image <path_to_image>" + reset)
		return cmdResult{}
	}
	imgPath := args
	info, err := os.Stat(imgPath)
	if err != nil || info.IsDir() {
		fmt.Printf("%sFile not found: %s%s\n", red, imgPath, reset)
		return cmdResult{}
	}
	mimeType := mime.TypeByExtension(filepath.Ext(imgPath))
	if !strings.HasPrefix(mimeType, "image/") {
		fmt.Printf("%s'%s' doesn't look like an image.%s\n", red, filepath.Base(imgPath), reset)
		return cmdResult{}
	}
	data, err := os.ReadFile(imgPath)
	if err != nil {
		fmt.Printf("%sError reading image: %v%s\n", red, err, reset)
		return cmdResult{}
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	s.pendingImages = append(s.pendingImages, b64)
	fmt.Printf("%sImage added: %s%s\n", green, filepath.Base(imgPath), reset)
	return cmdResult{}
}

func cmdPrompt(s *chatSession, args string) cmdResult {
	args = strings.TrimSpace(args)

	if args == "clear" || args == "off" {
		if s.pendingPrefix == "" {
			fmt.Println(dim + "No prompt loaded." + reset)
		} else {
			s.pendingPrefix = ""
			fmt.Println(yellow + "Pending prompt discarded." + reset)
		}
		return cmdResult{}
	}

	if s.cfg.PromptPath == "" {
		fmt.Println(red + "No prompt_path configured for this client." + reset)
		return cmdResult{}
	}

	var pf promptFile
	if args != "" {
		var err error
		pf, err = findPromptByName(s.cfg.PromptPath, args)
		if err != nil {
			fmt.Printf("%s%v%s\n", red, err, reset)
			return cmdResult{}
		}
	} else {
		list, err := listPromptFiles(s.cfg.PromptPath)
		if err != nil {
			fmt.Printf("%sError listing prompts: %v%s\n", red, err, reset)
			return cmdResult{}
		}
		if len(list) == 0 {
			fmt.Printf("%sNo prompts found in %s%s\n", yellow, s.cfg.PromptPath, reset)
			return cmdResult{}
		}
		fmt.Printf("\n%sAvailable prompts (from %s):%s\n", bold, s.cfg.PromptPath, reset)
		for i, p := range list {
			fmt.Printf("%s%3d%s  %-20s %s\n",
				cyan, i+1, reset, p.name, dim+p.preview+reset)
		}
		input := readLine("Select prompt (Enter to cancel): ")
		if input == "" {
			return cmdResult{}
		}
		idx, err := strconv.Atoi(input)
		if err != nil || idx < 1 || idx > len(list) {
			fmt.Printf("%sInvalid choice.%s\n", red, reset)
			return cmdResult{}
		}
		pf = list[idx-1]
	}

	content, err := pf.read()
	if err != nil {
		fmt.Printf("%sError reading prompt: %v%s\n", red, err, reset)
		return cmdResult{}
	}
	content = strings.TrimRight(content, " \t\r\n")
	if content == "" {
		fmt.Printf("%sPrompt '%s' is empty.%s\n", yellow, pf.name, reset)
		return cmdResult{}
	}

	s.pendingPrefix = content

	fmt.Printf("\n%sLoaded '%s'%s (%d lines, %d chars) — prepended to next message.\n",
		green, pf.name, reset, pf.lines, len(content))
	showPromptPreview(content)
	fmt.Println(dim + "Type your message (or /m for multi-line), Enter to send the prompt alone, /prompt clear to discard." + reset)
	return cmdResult{}
}

func showPromptPreview(content string) {
	lines := strings.Split(content, "\n")
	const maxShow = 300
	fmt.Printf("%s--- prompt ---%s\n", dim, reset)
	if len(lines) <= maxShow {
		fmt.Printf("%s%s%s\n", grey, content, reset)
	} else {
		for i := 0; i < maxShow-1; i++ {
			fmt.Printf("%s%s%s\n", grey, lines[i], reset)
		}
		fmt.Printf("%s… (%d more lines)%s\n",
			grey, len(lines)-(maxShow-1), reset)
	}
	fmt.Printf("%s--- end prompt ---%s\n", dim, reset)
}

func loadSessionInto(s *chatSession, file string) (Message, int, error) {
	loaded, loadedOpts, err := loadSession(file)
	if err != nil {
		return Message{}, 0, err
	}
	s.mu.Lock()
	s.messages = loaded
	s.currentOptions = make(map[string]interface{})
	for k, v := range loadedOpts {
		s.currentOptions[k] = v
	}
	count := len(s.messages)
	var last Message
	if count > 0 {
		last = s.messages[count-1]
	}
	s.mu.Unlock()
	return last, count, nil
}

func cmdControl(s *chatSession, args string) cmdResult {
	if args == "" {
		s.mu.Lock()
		mode := s.control
		pending := s.pending != ""
		s.mu.Unlock()
		pendingStr := "none"
		if pending {
			pendingStr = "block pending"
		}
		fmt.Printf("%scontrol: %s (%s)%s\n", yellow, mode, pendingStr, reset)
		return cmdResult{}
	}

	arg := strings.ToLower(strings.TrimSpace(args))
	s.mu.Lock()
	switch {
	case arg == "off":
		hadPending := s.pending != ""
		s.control = "off"
		s.autoRemaining = 0
		s.pending = ""
		s.pendingFrom = ""
		s.pendingTrace = ""
		s.mu.Unlock()
		if hadPending {
			fmt.Printf("%sControl mode: off (pending block discarded)%s\n", yellow, reset)
		} else {
			fmt.Printf("%sControl mode: off%s\n", yellow, reset)
		}
	case arg == "on" || arg == "manual":
		s.control = "on"
		s.autoRemaining = 0
		s.mu.Unlock()
		fmt.Printf("%sControl mode: on%s\n", green, reset)
	case arg == "auto":
		s.control = "auto"
		s.autoRemaining = -1
		s.mu.Unlock()
		fmt.Printf("%sControl mode: auto (unlimited)%s\n", green, reset)
	case strings.HasPrefix(arg, "auto "):
		nStr := strings.TrimSpace(arg[5:])
		n, err := strconv.Atoi(nStr)
		if err != nil || n < 0 {
			s.mu.Unlock()
			fmt.Printf("%sInvalid count: %s%s\n", red, nStr, reset)
			return cmdResult{}
		}
		if n == 0 {
			s.control = "auto"
			s.autoRemaining = -1
			s.mu.Unlock()
			fmt.Printf("%sControl mode: auto (unlimited)%s\n", green, reset)
		} else {
			s.control = fmt.Sprintf("auto %d", n)
			s.autoRemaining = n
			s.mu.Unlock()
			fmt.Printf("%sControl mode: auto (%d remaining)%s\n", green, n, reset)
		}
	default:
		s.mu.Unlock()
		fmt.Printf("%sUsage: /control | /control on|off|auto|auto N%s\n", red, reset)
	}
	return cmdResult{}
}

func cmdRouter(s *chatSession, args string) cmdResult {
	s.mu.Lock()
	r := s.router
	s.mu.Unlock()
	if r == nil {
		r = newRouter(false, nil)
	}

	if args == "" {
		if !r.enabled {
			fmt.Printf("%srouter: off%s\n", yellow, reset)
			return cmdResult{}
		}
		fmt.Printf("%srouter: on%s\n", green, reset)
		if len(r.peers) == 0 {
			fmt.Printf("  %s(no peers configured)%s\n", dim, reset)
			return cmdResult{}
		}
		for alias, url := range r.peers {
			fmt.Printf("  %s%s%s → %s\n", cyan, alias, reset, url)
		}
		return cmdResult{}
	}

	arg := strings.ToLower(strings.TrimSpace(args))
	switch arg {
	case "on", "true", "1", "yes":
		r.enabled = true
		if len(r.peers) == 0 {
			fmt.Printf("%sRouter: on (no peers configured — see config)%s\n", yellow, reset)
		} else {
			fmt.Printf("%sRouter: on%s\n", green, reset)
		}
	case "off", "false", "0", "no":
		r.enabled = false
		fmt.Printf("%sRouter: off%s\n", yellow, reset)
	default:
		fmt.Printf("%sUsage: /router on|off%s\n", red, reset)
	}
	return cmdResult{}
}

func cmdExec(s *chatSession, args string) cmdResult {
	s.mu.Lock()
	pending := s.pending
	from := s.pendingFrom
	trace := s.pendingTrace
	s.mu.Unlock()

	if pending == "" {
		fmt.Printf("%sNo pending control block.%s\n", yellow, reset)
		return cmdResult{}
	}

	env := Envelope{From: from, TraceID: trace}
	s.runControlBlock(pending, env)
	return cmdResult{}
}

func cmdDel(s *chatSession, args string) cmdResult {
	s.mu.Lock()
	s.pending = ""
	s.pendingFrom = ""
	s.pendingTrace = ""
	s.mu.Unlock()
	return cmdResult{}
}
