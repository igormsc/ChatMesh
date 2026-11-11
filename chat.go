package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sendState struct {
	mode      string // "manual" | "auto"
	remaining int    // -1 = unlimited, 0 = n/a, >0 = count
}

func (s *sendState) shouldFire() bool {
	if s.mode != "auto" {
		return false
	}
	if s.remaining == -1 {
		return true
	}
	if s.remaining > 0 {
		s.remaining--
		if s.remaining == 0 {
			s.mode = "manual"
		}
		return true
	}
	s.mode = "manual"
	return false
}

type chatSession struct {
	mu sync.Mutex

	messages        []Message
	currentOptions  map[string]interface{}
	model           string
	currentTrace    string
	sendState       sendState
	pending         string // raw control block, "" = none
	pendingFrom     string
	pendingTrace    string
	control         string // "off" | "on" | "auto" | "auto N"
	autoRemaining   int
	lastPromptTok   int
	lastEvalTok     int
	ctxWindowCached int // 0 = not fetched, -1 = unknown, >0 = window size
	router          *Router

	// main goroutine only
	thinkSetting  *bool
	pendingImages []string
	pendingPrefix string

	// immutable after construction
	cfg          *ClientConfig
	inboxCh      <-chan Envelope
	autosaveFile string
}

func newChatSession(model string, messages []Message, initialOptions map[string]interface{}, cfg *ClientConfig, inboxCh <-chan Envelope) *chatSession {
	opts := make(map[string]interface{}, len(initialOptions))
	for k, v := range initialOptions {
		opts[k] = v
	}

	var thinkSetting *bool
	if cfg.Think != nil {
		v := *cfg.Think
		thinkSetting = &v
	}

	safeModel := strings.ReplaceAll(model, "/", "_")
	autosaveFile := fmt.Sprintf("autosave_%s_%s_%d.json",
		cfg.InstanceName, safeModel, time.Now().Unix())

	s := &chatSession{
		messages:       messages,
		currentOptions: opts,
		model:          model,
		thinkSetting:   thinkSetting,
		cfg:            cfg,
		inboxCh:        inboxCh,
		autosaveFile:   autosaveFile,
		control:        cfg.Control,
		router:         newRouter(cfg.RouterEnabled, cfg.Peers),
	}
	if s.control == "" {
		s.control = "off"
	}
	if strings.HasPrefix(s.control, "auto ") {
		if n, err := strconv.Atoi(strings.TrimSpace(s.control[5:])); err == nil {
			s.autoRemaining = n
		}
	}
	s.sendState.mode = cfg.SendMode
	if s.sendState.mode == "auto" {
		if cfg.SendCount > 0 {
			s.sendState.remaining = cfg.SendCount
		} else {
			s.sendState.remaining = -1
		}
	}
	return s
}

func (s *chatSession) hasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending != ""
}

// ensureTrace returns the current trace, generating one if empty.
func (s *chatSession) ensureTrace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentTrace == "" {
		s.currentTrace = generateTraceID()
	}
	return s.currentTrace
}

func (s *chatSession) snapshot() ([]Message, map[string]interface{}, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := make([]Message, len(s.messages))
	copy(msgs, s.messages)
	opts := make(map[string]interface{}, len(s.currentOptions))
	for k, v := range s.currentOptions {
		opts[k] = v
	}
	return msgs, opts, s.model
}

func (s *chatSession) getModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

func (s *chatSession) setModel(m string) {
	s.mu.Lock()
	s.model = m
	s.mu.Unlock()
}

func (s *chatSession) appendMessage(msg Message) {
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
}

func (s *chatSession) popLastIfUser() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.messages); n > 0 && s.messages[n-1].Role == "user" {
		s.messages = s.messages[:n-1]
	}
}

func (s *chatSession) autosaveNow() {
	msgs, opts, _ := s.snapshot()
	saveSession(s.autosaveFile, msgs, opts)
}

func (s *chatSession) saveTimestamped(prefix string) {
	msgs, opts, m := s.snapshot()
	safeName := strings.ReplaceAll(m, "/", "_")
	name := fmt.Sprintf("%s_%s_%s", prefix, safeName, time.Now().Format("20060102_150405"))
	saveSession(name, msgs, opts)
}

func (s *chatSession) fireMessage(msg Message) error {
	if s.cfg.ForwardURL == "" {
		return fmt.Errorf("no forward_url configured")
	}
	trace := s.ensureTrace()
	env := Envelope{
		From:      s.cfg.InstanceName,
		TraceID:   trace,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Messages:  []Message{msg},
	}
	fmt.Printf("%s[send]%s %s → %s (trace %s)\n", cyan, reset, s.cfg.InstanceName, s.cfg.ForwardURL, trace)
	return sendEnvelope(s.cfg.ForwardURL, env)
}

func (s *chatSession) fireLatest() {
	s.mu.Lock()
	var last *Message
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == "assistant" {
			m := s.messages[i]
			last = &m
			break
		}
	}
	s.mu.Unlock()
	if last == nil {
		fmt.Println(red + "No assistant message to send yet." + reset)
		return
	}
	if err := s.fireMessage(*last); err != nil {
		fmt.Printf("%s[send] failed: %v%s\n", red, err, reset)
	}
}

func (s *chatSession) doGenerate() bool {
	reqMsgs, reqOpts, reqModel := s.snapshot()
	res, err := streamChat(ChatRequest{
		Model:    reqModel,
		Messages: reqMsgs,
		Stream:   true,
		Think:    s.thinkSetting,
		Options:  reqOpts,
	})
	if err != nil {
		fmt.Printf("%sError: %v%s\n", red, err, reset)
		//s.popLastMessage()
		s.popLastIfUser()
		return false
	}
	fmt.Println()

	if res.PromptTokens > 0 && res.EvalTokens > 0 {
		totalTokens := res.PromptTokens + res.EvalTokens
		durationSec := float64(res.TotalDuration) / 1e9
		tps := 0.0
		if durationSec > 0 {
			tps = float64(res.EvalTokens) / durationSec
		}
		fmt.Printf(
			"%s%sPrompt tokens: %d | Generated tokens: %d | Total tokens: %d | Speed: %.2f tok/s | Time: %.1fs%s\n",
			grey, dim, res.PromptTokens, res.EvalTokens, totalTokens, tps, durationSec, reset,
		)
	}

	s.mu.Lock()
	s.lastPromptTok = res.PromptTokens
	s.lastEvalTok = res.EvalTokens
	s.mu.Unlock()

	assistantMsg := Message{Role: "assistant", Content: res.Content}
	if res.Reasoning != "" {
		assistantMsg.Reasoning = res.Reasoning
	}
	if len(res.ExtraTokens) > 0 {
		assistantMsg.Extra = res.ExtraTokens
	}
	s.appendMessage(assistantMsg)
	s.autosaveNow()

	if alias, body, hasAddr := extractAddress(assistantMsg.Content); hasAddr {
		s.mu.Lock()
		router := s.router
		s.mu.Unlock()

		if router == nil || !router.enabled {
			fmt.Printf("%s[router] @%s directive but router is off — not sent%s\n",
				yellow, alias, reset)
			return true
		}
		if body == "" {
			fmt.Printf("%s[router] @%s with empty body — not routed%s\n",
				red, alias, reset)
			return true
		}
		traceID := s.ensureTrace()
		if err := router.Route(alias, body, s.cfg.InstanceName, traceID); err != nil {
			fmt.Printf("%s[router] %v%s\n", red, err, reset)
			return true
		}
		s.mu.Lock()
		if n := len(s.messages); n > 0 {
			s.messages[n-1].Content = fmt.Sprintf("[→%s: %s]", alias, firstLine(body, 80))
		}
		s.mu.Unlock()
		s.autosaveNow()
		return true
	}

	s.mu.Lock()
	fire := s.sendState.shouldFire()
	s.mu.Unlock()
	if fire {
		if err := s.fireMessage(assistantMsg); err != nil {
			fmt.Printf("%s[send] failed: %v%s\n", red, err, reset)
		}
	}
	return true
}

func (s *chatSession) handleInbox(env Envelope) {
	text, ok := ingestEnvelope(env)
	if !ok {
		fmt.Printf("%s[inbox] message from %s has no assistant content, ignored%s\n",
			yellow, env.From, reset)
		return
	}

	s.mu.Lock()
	control := s.control
	s.mu.Unlock()
	if control != "off" {
		if block, hasCtrl := extractControl(text); hasCtrl {
			if strings.TrimSpace(block) == "" {
				fmt.Printf("%s[control] empty block from %s, ignored%s\n",
					yellow, env.From, reset)
				return
			}
			s.handleControl(block, env)
			return
		}
	}

	s.mu.Lock()
	s.currentTrace = env.TraceID
	s.mu.Unlock()

	fmt.Printf("\n%s[from %s | trace %s | %s]%s\n",
		cyan, env.From, env.TraceID, env.Timestamp, reset)
	fmt.Printf("%s%s%s\n", dim, text, reset)

	s.appendMessage(Message{Role: "user", Content: text})
	s.autosaveNow()
	s.doGenerate()
}

func chatLoop(model string, messages []Message, initialOptions map[string]interface{}, cfg *ClientConfig, inboxCh <-chan Envelope) {
	fmt.Printf("\n%sChatting with %s%s%s\n", bold, green, model, reset)
	fmt.Println("Commands: /bye, /stop, /list, /model <name>, /save [name], /load <file>, /clear, /image, /m, /set [key value], /think [on|off], /info, /show, /send [auto [N]]")

	s := newChatSession(model, messages, initialOptions, cfg, inboxCh)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	stopSignal := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			signal.Stop(sigCh)
			msgs, opts, m := s.snapshot()
			safeName := strings.ReplaceAll(m, "/", "_")
			autoName := fmt.Sprintf("interrupt_%s_%s", safeName, time.Now().Format("20060102_150405"))
			saveSession(autoName, msgs, opts)
			fmt.Println(bold + "\nInterrupted — session saved." + reset)
			os.Exit(0)
		case <-stopSignal:
		}
	}()
	defer func() {
		close(stopSignal)
		signal.Stop(sigCh)
	}()

	for {
		var promptStr string
		switch {
		case s.hasPending():
			promptStr = "[pending] > "
		case s.pendingPrefix != "" && len(s.pendingImages) > 0:
			promptStr = fmt.Sprintf("[prompt, %d img] > ", len(s.pendingImages))
		case s.pendingPrefix != "":
			promptStr = "[prompt] > "
		case len(s.pendingImages) > 0:
			promptStr = fmt.Sprintf("[%d img] > ", len(s.pendingImages))
		default:
			promptStr = "> "
		}
		fmt.Print(promptStr)

		var input string
		select {
		case line, ok := <-stdinCh:
			if !ok {
				s.saveTimestamped("auto")
				fmt.Println(bold + "\nGoodbye!" + reset)
				os.Exit(0)
			}
			input = line
		case env := <-s.inboxCh:
			s.handleInbox(env)
			continue
		}

		promptOnly := false
		if input == "" {
			if s.pendingPrefix == "" {
				continue
			}
			fmt.Printf("%sSending prompt alone (%d chars).%s\n",
				dim, len(s.pendingPrefix), reset)
			input = s.pendingPrefix
			s.pendingPrefix = ""
			promptOnly = true
		}

		if !promptOnly && strings.HasPrefix(input, "/") {
			parts := strings.SplitN(input[1:], " ", 2)
			cmd := parts[0]
			args := ""
			if len(parts) > 1 {
				args = parts[1]
			}
			handler, ok := commands[cmd]
			if !ok {
				fmt.Printf("%sUnknown command: /%s%s\n", red, cmd, reset)
				continue
			}
			res := handler(s, args)
			if res.stopLoop {
				return
			}
			if res.replacedInput == "" {
				continue
			}
			input = res.replacedInput
		}

		content := input
		if s.pendingPrefix != "" {
			fmt.Printf("%sPrepending prompt (%d chars) to your message.%s\n",
				dim, len(s.pendingPrefix), reset)
			content = s.pendingPrefix + "\n\n" + content
			s.pendingPrefix = ""
		}

		userMsg := Message{Role: "user", Content: input}
		if len(s.pendingImages) > 0 {
			userMsg.Images = s.pendingImages
			s.pendingImages = nil
			fmt.Printf("%sAttached %d image(s).%s\n", dim, len(userMsg.Images), reset)
		}
		s.mu.Lock()
		s.currentTrace = "" // fresh trace for user-initiated generation
		s.messages = append(s.messages, userMsg)
		s.mu.Unlock()
		s.autosaveNow()
		s.doGenerate()
	}
}
