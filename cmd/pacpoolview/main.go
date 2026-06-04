package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9819", "HTTP listen address")
	pacpoolURL := flag.String("pacpool", "http://127.0.0.1:9809", "pacpool HTTP base URL")
	pacdURL := flag.String("pacd", "http://127.0.0.1:9509", "pacd RPC base URL")
	timeout := flag.Duration("timeout", 3*time.Second, "upstream request timeout")
	flag.Parse()

	proxy := &proxy{
		pacpoolURL: strings.TrimRight(*pacpoolURL, "/"),
		pacdURL:    strings.TrimRight(*pacdURL, "/"),
		client: &http.Client{
			Timeout: *timeout,
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", proxy.handleIndex)
	mux.HandleFunc("/status", proxy.handleStatus)
	mux.HandleFunc("/healthz", proxy.handleHealthz)
	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("pacpoolview listening on http://%s pacpool=%s pacd=%s", *listen, proxy.pacpoolURL, proxy.pacdURL)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type proxy struct {
	pacpoolURL string
	pacdURL    string
	client     *http.Client
}

func (p *proxy) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, err := p.mergedStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"healthy":    truthy(status["healthy"]),
		"updated_at": time.Now().UTC(),
	})
}

func (p *proxy) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/status" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, err := p.mergedStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, status)
}

func (p *proxy) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := p.fetchBytes(r.Context(), p.pacpoolURL+"/")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	status, err := p.mergedStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := rewriteIndex(string(body), status)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, out)
}

func (p *proxy) mergedStatus(ctx context.Context) (map[string]any, error) {
	status, err := p.fetchJSON(ctx, p.pacpoolURL+"/status")
	if err != nil {
		return nil, fmt.Errorf("pacpool status: %w", err)
	}
	stratum, err := p.fetchJSON(ctx, p.pacdURL+"/getstratuminfo")
	if err != nil {
		return nil, fmt.Errorf("pacd stratum info: %w", err)
	}
	mergeStratum(status, stratum)
	return status, nil
}

func (p *proxy) fetchJSON(ctx context.Context, url string) (map[string]any, error) {
	data, err := p.fetchBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *proxy) fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func mergeStratum(status map[string]any, stratum map[string]any) {
	pool := ensureMap(status, "pool")
	connected := intValue(stratum["connected_miners"])
	pool["connected_miners"] = connected
	pool["ready_for_stratum"] = truthy(stratum["enabled"])
	if diff := floatValue(stratum["share_difficulty"]); diff > 0 {
		pool["share_difficulty"] = diff
	}
	pool["live_stratum_source"] = "pacd"
	pool["live_stratum"] = stratum
	status["stratum"] = stratum
	status["healthy"] = truthy(status["healthy"]) && truthy(stratum["enabled"])

	oldWorkers := workersByName(pool["workers"])
	merged := workersSlice(pool["workers"])
	for _, live := range workersSlice(stratum["workers"]) {
		name := stringValue(live["name"])
		if name == "" {
			continue
		}
		target := oldWorkers[name]
		if target == nil {
			target = map[string]any{
				"name":          name,
				"accepted":      0,
				"accepted_work": 0,
				"rejected":      0,
				"solved_blocks": 0,
			}
			merged = append(merged, target)
			oldWorkers[name] = target
		}
		target["online"] = true
		target["difficulty"] = coalesceNumber(live["difficulty"], target["difficulty"])
		target["pacd_accepted"] = intValue(live["accepted"])
		target["pacd_rejected"] = intValue(live["rejected"])
		target["sessions"] = intValue(live["sessions"])
		target["remote_addrs"] = live["remote_addrs"]
		if last := stringValue(live["last_share_at"]); last != "" && !strings.HasPrefix(last, "0001-") {
			target["last_share_at"] = last
		} else if seen := stringValue(live["last_seen_at"]); seen != "" && !strings.HasPrefix(seen, "0001-") {
			target["last_share_at"] = seen
		}
		if seen := stringValue(live["last_seen_at"]); seen != "" {
			target["last_seen_at"] = seen
		}
	}
	pool["workers"] = merged
}

func rewriteIndex(page string, status map[string]any) string {
	pool := mapValue(status["pool"])
	connected := intValue(pool["connected_miners"])
	page = regexp.MustCompile(`(?s)(<div class="label">Miners</div><div class="value">)[^<]*(</div>)`).
		ReplaceAllString(page, "${1}"+strconv.Itoa(connected)+"${2}")

	return rewriteWorkerRows(page, workersSlice(pool["workers"]))
}

func rewriteWorkerRows(page string, workers []map[string]any) string {
	if len(workers) == 0 {
		return page
	}
	idx := strings.Index(page, "<h2>Workers</h2>")
	if idx < 0 {
		return page
	}
	open := strings.Index(page[idx:], "<tbody>")
	if open < 0 {
		return page
	}
	openAt := idx + open + len("<tbody>")
	closeRel := strings.Index(page[openAt:], "</tbody>")
	if closeRel < 0 {
		return page
	}
	closeAt := openAt + closeRel
	var rows strings.Builder
	for _, worker := range workers {
		name := html.EscapeString(stringValue(worker["name"]))
		if name == "" {
			continue
		}
		if truthy(worker["online"]) {
			name += `<br><span class="pill ok">Online</span>`
		}
		rows.WriteString(fmt.Sprintf(
			"\n<tr><td>%s</td><td>%.2f</td><td>%d</td><td>%d</td><td>%s</td></tr>",
			name,
			floatValue(worker["difficulty"]),
			intValue(worker["accepted"])+intValue(worker["pacd_accepted"]),
			intValue(worker["rejected"])+intValue(worker["pacd_rejected"]),
			html.EscapeString(shortTime(stringValue(worker["last_share_at"]))),
		))
	}
	rows.WriteByte('\n')
	return page[:openAt] + rows.String() + page[closeAt:]
}

func workersByName(v any) map[string]map[string]any {
	out := make(map[string]map[string]any)
	for _, worker := range workersSlice(v) {
		if name := stringValue(worker["name"]); name != "" {
			out[name] = worker
		}
	}
	return out
}

func workersSlice(v any) []map[string]any {
	if typed, ok := v.([]map[string]any); ok {
		return typed
	}
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m := mapValue(item); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func ensureMap(parent map[string]any, key string) map[string]any {
	if m := mapValue(parent[key]); m != nil {
		return m
	}
	m := make(map[string]any)
	parent[key] = m
	return m
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func intValue(v any) int {
	switch value := v.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case uint64:
		if value > uint64(math.MaxInt) {
			return math.MaxInt
		}
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func floatValue(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case uint64:
		return float64(value)
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func coalesceNumber(primary any, fallback any) any {
	if floatValue(primary) > 0 {
		return primary
	}
	return fallback
}

func truthy(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return value == "true" || value == "1"
	case float64:
		return value != 0
	case int:
		return value != 0
	default:
		return false
	}
}

func shortTime(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  message,
		"status": status,
	})
}
