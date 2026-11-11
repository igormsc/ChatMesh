package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var controlFenceRe = regexp.MustCompile("(?is)```control[ \t]*\r?\n(.*?)```")

type controlCmd struct {
	op   string
	args []string
	raw  string // normalized display form: "op arg1 arg2"
}

func extractControl(content string) (string, bool) {
	ms := controlFenceRe.FindAllStringSubmatch(content, -1)
	if len(ms) == 0 {
		return "", false
	}
	return strings.TrimRight(ms[0][1], "\r\n \t"), true
}

func parseControlBlock(block string) ([]controlCmd, error) {
	var cmds []controlCmd
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		cmds = append(cmds, controlCmd{
			op:   parts[0],
			args: parts[1:],
			raw:  strings.Join(parts, " "),
		})
	}
	if len(cmds) == 0 {
		return nil, fmt.Errorf("empty control block")
	}
	return cmds, nil
}

type controlResult struct {
	raw  string
	ok   bool
	note string
}

func (s *chatSession) handleControl(block string, env Envelope) {
	s.mu.Lock()
	mode := s.control
	s.mu.Unlock()

	switch {
	case mode == "on":
		s.mu.Lock()
		if s.pending != "" {
			s.mu.Unlock()
			fmt.Printf("%s[control] block from %s dropped (previous block still pending)%s\n",
				red, env.From, reset)
			return
		}
		s.pending = block
		s.pendingFrom = env.From
		s.pendingTrace = env.TraceID
		s.mu.Unlock()
		fmt.Printf("%s[control] block from %s pending — /exec to run, /del to discard%s\n",
			yellow, env.From, reset)

	case mode == "auto":
		s.runControlBlock(block, env)

	case strings.HasPrefix(mode, "auto "):
		s.mu.Lock()
		if s.autoRemaining <= 0 {
			s.control = "on"
			s.pending = block
			s.pendingFrom = env.From
			s.pendingTrace = env.TraceID
			s.mu.Unlock()
			fmt.Printf("%s[control] auto budget exhausted — now on (manual)%s\n", yellow, reset)
			fmt.Printf("%s[control] block from %s pending — /exec to run, /del to discard%s\n",
				yellow, env.From, reset)
			return
		}
		s.autoRemaining--
		s.mu.Unlock()
		s.runControlBlock(block, env)

	default:
		fmt.Printf("%s[control] block from %s ignored (control=%s)%s\n",
			yellow, env.From, mode, reset)
	}
}

func (s *chatSession) runControlBlock(block string, env Envelope) {
	defer func() {
		s.mu.Lock()
		s.pending = ""
		s.pendingFrom = ""
		s.pendingTrace = ""
		s.mu.Unlock()
	}()

	cmds, err := parseControlBlock(block)
	if err != nil {
		fmt.Printf("%s[control] malformed block from %s: %v%s\n", red, env.From, err, reset)
		return
	}

	results := make([]controlResult, 0, len(cmds))
	for _, c := range cmds {
		results = append(results, s.execControlCmd(c))
	}

	feedback := formatControlFeedback(results, env)
	fmt.Printf("%s[control] block from %s executed (%d commands)%s\n",
		green, env.From, len(results), reset)

	if s.cfg.ForwardURL == "" {
		fmt.Printf("%s[control] no forward_url; feedback not sent%s\n", yellow, reset)
		fmt.Println(feedback)
		return
	}
	feedbackEnv := Envelope{
		From:      s.cfg.InstanceName,
		TraceID:   env.TraceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Messages:  []Message{{Role: "assistant", Content: feedback}},
	}
	if err := sendEnvelope(s.cfg.ForwardURL, feedbackEnv); err != nil {
		fmt.Printf("%s[control] feedback send failed: %v%s\n", red, err, reset)
	}
}

func (s *chatSession) execControlCmd(c controlCmd) controlResult {
	switch c.op {
	case "clear_context":
		s.mu.Lock()
		before := len(s.messages)
		s.messages = nil
		s.lastPromptTok = 0
		s.lastEvalTok = 0
		s.mu.Unlock()
		return controlResult{
			raw:  c.raw,
			ok:   true,
			note: fmt.Sprintf("ok (%d msgs → 0)", before),
		}
	case "status":
		return controlResult{raw: c.raw, ok: true, note: s.statusLine()}
	default:
		return controlResult{
			raw:  c.raw,
			ok:   false,
			note: fmt.Sprintf("rejected (unknown command '%s'; valid: clear_context, status)", c.op),
		}
	}
}

func (s *chatSession) statusLine() string {
	s.mu.Lock()
	model := s.model
	nMsgs := len(s.messages)
	used := s.lastPromptTok + s.lastEvalTok
	s.mu.Unlock()

	window := s.ctxWindow()
	if window > 0 {
		return fmt.Sprintf("model=%s, msgs=%d, ctx=%d/%d, left=%d",
			model, nMsgs, used, window, window-used)
	}
	return fmt.Sprintf("model=%s, msgs=%d, ctx=%d, window=unknown",
		model, nMsgs, used)
}

func (s *chatSession) ctxWindow() int {
	s.mu.Lock()
	if v, ok := s.currentOptions["num_ctx"]; ok {
		s.mu.Unlock()
		switch n := v.(type) {
		case int64:
			return int(n)
		case int:
			return n
		case float64:
			return int(n)
		}
		return -1
	}
	if s.ctxWindowCached != 0 {
		v := s.ctxWindowCached
		s.mu.Unlock()
		return v
	}
	model := s.model
	s.mu.Unlock()

	info, err := fetchModelInfo(model)
	if err != nil {
		s.mu.Lock()
		s.ctxWindowCached = -1
		s.mu.Unlock()
		return -1
	}

	window := -1
	for k, v := range info.ModelInfo {
		if strings.HasSuffix(k, ".context_length") || k == "context_length" {
			if f, ok := v.(float64); ok {
				window = int(f)
			}
			break
		}
	}
	if window <= 0 && len(info.Parameters) > 0 {
		var params map[string]interface{}
		if err := json.Unmarshal(info.Parameters, &params); err == nil {
			if v, ok := params["num_ctx"].(float64); ok {
				window = int(v)
			}
		}
	}
	if window <= 0 {
		window = -1
	}

	s.mu.Lock()
	s.ctxWindowCached = window
	s.mu.Unlock()
	return window
}

func formatControlFeedback(results []controlResult, env Envelope) string {
	if len(results) == 1 {
		r := results[0]
		return fmt.Sprintf("[control] %s on %s: %s", r.raw, env.From, r.note)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[control] block on %s:\n", env.From)
	for _, r := range results {
		fmt.Fprintf(&b, "  %s: %s\n", r.raw, r.note)
	}
	return strings.TrimRight(b.String(), "\n")
}
