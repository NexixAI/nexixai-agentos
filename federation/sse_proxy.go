package federation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SSEProxy struct {
	Client *http.Client
}

func NewSSEProxy() *SSEProxy {
	return &SSEProxy{Client: &http.Client{Timeout: 0}} // streaming; no timeout
}

// Proxy streams SSE from remote to the client, with lightweight event_id dedupe per-connection.
func (p *SSEProxy) Proxy(w http.ResponseWriter, remoteEventsURL string, tenantID string, principalID string, bearerToken string, fromSequence int) error {
	targetURL := addFromSequence(remoteEventsURL, fromSequence)

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Tenant-Id", tenantID)
	if principalID != "" {
		req.Header.Set("X-Principal-Id", principalID)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	var (
		resp    *http.Response
		lastErr error
	)
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err = p.Client.Do(req)
		if err == nil {
			break
		}
		lastErr = err
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	if err != nil {
		return lastErr
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote events returned %s", resp.Status)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported by response writer")
	}

	seen := map[string]struct{}{}
	lastSeq := fromSequence

	reader := bufio.NewReader(resp.Body)
	bw := bufio.NewWriter(w)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				_ = bw.Flush()
				flusher.Flush()
				return nil
			}
			return err
		}

		trim := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trim, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trim, "data:"))
			// attempt dedupe if payload is an EventEnvelope
			var env map[string]any
			if json.Unmarshal([]byte(payload), &env) == nil {
				if eventObj, ok := env["event"].(map[string]any); ok {
					eid, _ := eventObj["event_id"].(string)
					seq := 0
					if v, ok := eventObj["sequence"].(float64); ok {
						seq = int(v)
					}
					if eid != "" {
						if _, exists := seen[eid]; exists {
							// drop duplicate event
							continue
						}
						seen[eid] = struct{}{}
					}
					if seq != 0 && seq <= lastSeq {
						// enforce monotonic sequence
						continue
					}
					if seq > lastSeq {
						lastSeq = seq
					}
				}
			}
		}

		_, _ = bw.WriteString(line)
		if trim == "" {
			_ = bw.Flush()
			flusher.Flush()
		}
	}
}

func StreamStoredEvents(w http.ResponseWriter, envelopes []map[string]any) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported by response writer")
	}

	bw := bufio.NewWriter(w)
	for _, env := range envelopes {
		b, _ := json.Marshal(env)
		_, _ = bw.WriteString("event: agentos.event\n")
		_, _ = bw.WriteString("data: " + string(b) + "\n\n")
	}
	_ = bw.Flush()
	flusher.Flush()

	// small delay so clients have time to read before close (helps curl demos)
	time.Sleep(25 * time.Millisecond)
	return nil
}

func addFromSequence(remoteEventsURL string, fromSequence int) string {
	if fromSequence <= 0 {
		return remoteEventsURL
	}
	parsed, err := url.Parse(remoteEventsURL)
	if err != nil {
		return remoteEventsURL
	}
	q := parsed.Query()
	q.Set("from_sequence", strconv.Itoa(fromSequence))
	parsed.RawQuery = q.Encode()
	return parsed.String()
}
