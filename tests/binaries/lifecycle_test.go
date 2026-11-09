package binaries_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// serviceTestEnv keeps the caller's normal process environment but removes application settings so a developer's .env cannot override the ports or configuration intentionally supplied by each lifecycle test.
func serviceTestEnv(overrides ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || isServiceSetting(key) {
			continue
		}
		env = append(env, entry)
	}
	return append(env, overrides...)
}

func isServiceSetting(key string) bool {
	if strings.HasPrefix(key, "NEPSE_") || strings.HasPrefix(key, "REST_") ||
		strings.HasPrefix(key, "MCP_") || strings.HasPrefix(key, "LOG_") || strings.HasPrefix(key, "SHUTDOWN_") {
		return true
	}
	switch key {
	case "SERVICE_NAME", "TRUSTED_PROXIES":
		return true
	default:
		return false
	}
}

func build(t *testing.T, name string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", out, "../../src/cmd/"+name)
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	return out
}

func start(t *testing.T, binary string, env ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	cmd := exec.Command(binary)
	cmd.Env = serviceTestEnv(env...)
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, &output
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not become ready", url)
}

func stopGracefully(t *testing.T, cmd *exec.Cmd, output *bytes.Buffer, signal os.Signal, expectedLog string) {
	t.Helper()
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatalf("signal process: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("process exit: %v\n%s", err, output.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("process did not shut down gracefully")
	}
	if !strings.Contains(output.String(), expectedLog) {
		t.Fatalf("expected shutdown log %q:\n%s", expectedLog, output.String())
	}
}

func TestRESTBinaryLifecycle(t *testing.T) {
	binary := build(t, "rest")
	port := freePort(t)
	cmd, output := start(t, binary, fmt.Sprintf("REST_PORT=%d", port), "LOG_LEVEL=info")
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, baseURL+"/health/ready")
	stopGracefully(t, cmd, output, os.Interrupt, "rest server shutting down")
}

func TestMCPBinaryLifecycleWithoutCallerAuthentication(t *testing.T) {
	binary := build(t, "mcp")
	port := freePort(t)
	cmd, output := start(t, binary, fmt.Sprintf("MCP_PORT=%d", port))
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, baseURL+"/health/ready")

	// Use a protocol-compliant client so the initialize handshake happens before
	// tools/list. The transport deliberately sends no Authorization header.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "nepse-binary-lifecycle-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("MCP connect without caller credentials: %v\n%s", err, output.String())
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		t.Fatalf("MCP tools/list without caller credentials: %v", err)
	}
	foundSummary := false
	for _, tool := range tools.Tools {
		if tool.Name == "get_market_summary" {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		_ = session.Close()
		t.Fatal("MCP tools/list did not include get_market_summary")
	}
	if err := session.Close(); err != nil {
		t.Errorf("close MCP session: %v", err)
	}
	stopGracefully(t, cmd, output, os.Interrupt, "mcp server shutting down")
}

func TestBinariesRejectInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		binary string
		env    []string
		want   string
	}{
		{"rest", []string{"REST_ALLOWED_ORIGINS=*", "REST_RATE_LIMIT_PER_MINUTE=abc"}, "REST_ALLOWED_ORIGINS"},
		{"mcp", []string{"MCP_PORT=99999"}, "MCP_PORT"},
		{"mcp", []string{"NEPSE_BASE_URL=http://example.com"}, "NEPSE_BASE_URL"},
	} {
		binary := build(t, test.binary)
		cmd := exec.Command(binary)
		cmd.Env = serviceTestEnv(test.env...)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), test.want) {
			t.Errorf("%s with %v: expected rejection mentioning %s, err=%v\n%s", test.binary, test.env, test.want, err, output)
		}
	}
}
