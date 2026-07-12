package mcpstdio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/repowire/repowire/daemon-go/config"
	"github.com/repowire/repowire/daemon-go/hooks"
)

// Run proxies newline-delimited MCP stdio messages to the daemon's stateless
// Streamable HTTP endpoint while preserving the hosting runtime's identity.
func Run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "repowire mcp:", err)
		return 1
	}
	identity, proof := hooks.MCPIdentityProof()
	endpoint := fmt.Sprintf("http://%s:%d/mcp", cfg.Daemon.Host, cfg.Daemon.Port)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	client := &http.Client{}
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(line))
		if err != nil {
			writeError(writer, line, err.Error())
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("X-Repowire-Peer", identity)
		if proof != "" {
			req.Header.Set("X-Repowire-Identity-Proof", proof)
		}
		if cfg.Daemon.AuthToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.Daemon.AuthToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			writeError(writer, line, err.Error())
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			writeError(writer, line, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body)))
			continue
		}
		if len(bytes.TrimSpace(body)) > 0 {
			writer.Write(bytes.TrimSpace(body)) //nolint:errcheck
			writer.WriteByte('\n')              //nolint:errcheck
			writer.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "repowire mcp:", err)
		return 1
	}
	return 0
}

func writeError(writer *bufio.Writer, request []byte, message string) {
	var envelope struct {
		ID any `json:"id"`
	}
	_ = json.Unmarshal(request, &envelope)
	if envelope.ID == nil {
		return
	}
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": envelope.ID,
		"error": map[string]any{"code": -32000, "message": message},
	})
	writer.Write(raw)      //nolint:errcheck
	writer.WriteByte('\n') //nolint:errcheck
	writer.Flush()
}
