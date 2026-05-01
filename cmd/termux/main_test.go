package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func writeFile(path, body string, mode os.FileMode) error {
	return os.WriteFile(path, []byte(body), mode)
}

func pathEnv() string { return os.Getenv("PATH") }

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestHandleRunCommandJSON(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "echo hi"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if strings.TrimSpace(got.Stdout) != "hi" {
		t.Errorf("stdout: %q", got.Stdout)
	}
	if got.ExitCode != 0 {
		t.Errorf("exit_code: %d", got.ExitCode)
	}
}

func TestHandleRunCommandNonZeroIsErrorResult(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "exit 5"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected error result, got success: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.ExitCode != 5 {
		t.Errorf("exit_code: %d", got.ExitCode)
	}
}

func TestHandleRunCommandMissingCommand(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected error for missing command")
	}
}

func TestHandleRunCommandEnvExtraAndCwd(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	dir := t.TempDir()
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "echo $X"},
		"env_extra": map[string]any{
			"X": "from-env",
		},
		"cwd": dir,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if strings.TrimSpace(got.Stdout) != "from-env" {
		t.Errorf("env_extra didn't propagate: %q", got.Stdout)
	}
}

func TestHandleRunCommandTimeoutCap(t *testing.T) {
	requireSh(t)
	t.Setenv(allowlistEnv, "")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command":         "/bin/sh",
		"args":            []any{"-c", "sleep 30"},
		"timeout_seconds": float64(1),
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !got.TimedOut {
		t.Errorf("expected timed_out, got %+v", got)
	}
}

func TestHandleRunCommandAllowlistBlocks(t *testing.T) {
	t.Setenv(allowlistEnv, "ls,echo")
	res, err := handleRunCommand(context.Background(), callRequest(map[string]any{
		"command": "/bin/sh",
		"args":    []any{"-c", "echo nope"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatal("expected allowlist block to surface as error result")
	}
	if !strings.Contains(text, allowlistEnv) {
		t.Errorf("expected %s in error, got %q", allowlistEnv, text)
	}
}

func TestHandleInstallPkgUsesDoubleDash(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	// We can't run real `pkg`, so use a fake command via PATH override and
	// inspect the recorded args. Easier path: run the helper directly and
	// check the constructed args string.
	dir := t.TempDir()
	fake := dir + "/pkg"
	// Create a tiny shell shim that echoes its args.
	if err := writeFile(fake, "#!/bin/sh\necho \"$@\"\n", 0o755); err != nil {
		t.Fatalf("shim: %v", err)
	}
	t.Setenv("PATH", dir+":"+pathEnv())
	res, err := handleInstallPkg(context.Background(), callRequest(map[string]any{
		"package": "-evil-flag",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if !strings.Contains(got.Stdout, "install -y -- -evil-flag") {
		t.Errorf("expected `--` separator before package, got %q", got.Stdout)
	}
}

func TestHandleListPkgsTrusted(t *testing.T) {
	t.Setenv(allowlistEnv, "ls") // "pkg" is not allowlisted
	dir := t.TempDir()
	fake := dir + "/pkg"
	if err := writeFile(fake, "#!/bin/sh\necho list-installed-output\n", 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+pathEnv())
	res, err := handleListPkgs(context.Background(), callRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("trusted handler should bypass allowlist, got error: %s", text)
	}
	var got execResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !strings.Contains(got.Stdout, "list-installed-output") {
		t.Errorf("unexpected stdout: %q", got.Stdout)
	}
}

func TestHandleReadEnvSingle(t *testing.T) {
	t.Setenv("DROIDMCP_TEST_RE", "yes")
	res, err := handleReadEnv(context.Background(), callRequest(map[string]any{
		"name": "DROIDMCP_TEST_RE",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got envResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.Name != "DROIDMCP_TEST_RE" || got.Value != "yes" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestHandleReadEnvAll(t *testing.T) {
	t.Setenv("DROIDMCP_TEST_RE_DUMP", "found")
	res, err := handleReadEnv(context.Background(), callRequest(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := resultText(t, res)
	var got envResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.Vars["DROIDMCP_TEST_RE_DUMP"] != "found" {
		t.Errorf("expected env dump to include the test var, got %d entries", len(got.Vars))
	}
}

func TestStringMapArgDropsNonStrings(t *testing.T) {
	got := stringMapArg(callRequest(map[string]any{
		"env_extra": map[string]any{"A": "1", "B": 2, "C": "3"},
	}), "env_extra")
	if got["A"] != "1" || got["C"] != "3" {
		t.Fatalf("unexpected: %+v", got)
	}
	if _, ok := got["B"]; ok {
		t.Errorf("non-string value should be dropped")
	}
}

func TestTimeoutFromReqClamps(t *testing.T) {
	d := timeoutFromReq(callRequest(map[string]any{
		"timeout_seconds": float64(99999),
	}))
	if d != maxExecTimeout {
		t.Errorf("expected clamp to %v, got %v", maxExecTimeout, d)
	}
	d = timeoutFromReq(callRequest(map[string]any{}))
	if d != 0 {
		t.Errorf("expected 0 (default), got %v", d)
	}
}
