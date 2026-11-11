package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Envelope struct {
	From      string    `json:"from"`
	TraceID   string    `json:"trace_id"`
	Timestamp string    `json:"timestamp"`
	Messages  []Message `json:"messages"`
}

func startInboxServer(port int) (chan Envelope, error) {
	inboxCh := make(chan Envelope, 16)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	expectedHosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", port): true,
		fmt.Sprintf("localhost:%d", port): true,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/inbox", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !expectedHosts[r.Host] {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		ct := r.Header.Get("Content-Type")
		var env Envelope
		if strings.HasPrefix(ct, "text/plain") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read error", http.StatusBadRequest)
				return
			}
			env = Envelope{
				From:      "unknown",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Messages:  []Message{{Role: "assistant", Content: string(body)}},
			}
		} else {
			if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		if env.Timestamp == "" {
			env.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}

		select {
		case inboxCh <- env:
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		default:
			http.Error(w, "queue full", http.StatusTooManyRequests)
		}
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("%s[inbox] server error: %v%s\n", red, err, reset)
		}
	}()
	return inboxCh, nil
}

func ingestEnvelope(env Envelope) (string, bool) {
	for i := len(env.Messages) - 1; i >= 0; i-- {
		if env.Messages[i].Role == "assistant" && env.Messages[i].Content != "" {
			return env.Messages[i].Content, true
		}
	}
	return "", false
}

func sendEnvelope(forwardURL string, env Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(forwardURL, "application/json", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
