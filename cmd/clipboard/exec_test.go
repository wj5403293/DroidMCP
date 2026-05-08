package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh not available: %v", err)
	}
}

func TestEnsureBinariesMissingMessage(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	err := ensureBinaries("termux-clipboard-get")
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !strings.Contains(err.Error(), "termux-api") {
		t.Errorf("expected hint about termux-api, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "termux-clipboard-get") {
		t.Errorf("expected error to name the missing binary, got %q", err.Error())
	}
}

func TestEnsureBinariesPresent(t *testing.T) {
	prev := lookPath
	lookPath = func(name string) (string, error) { return "/fake/path/" + name, nil }
	t.Cleanup(func() { lookPath = prev })

	if err := ensureBinaries("termux-clipboard-get", "termux-clipboard-set"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunClipboardCmdRoundtrip(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "echo-stdin")
	// Read all stdin and echo back to stdout.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := runClipboardCmd(context.Background(), bin, nil, []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit_code: %d, stderr: %q", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "hello world" {
		t.Errorf("stdout: %q", res.Stdout)
	}
	if string(res.RawStdout) != "hello world" {
		t.Errorf("raw_stdout: %q", res.RawStdout)
	}
}

func TestRunClipboardCmdNonZeroExit(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "fail")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho oops 1>&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := runClipboardCmd(context.Background(), bin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit_code: %d", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "oops") {
		t.Errorf("stderr: %q", res.Stderr)
	}
}

func TestRunClipboardCmdInvalidUTF8(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	// Stash the raw non-UTF-8 bytes in a file and cat it from the shim.
	// printf '\xff' is not portable across all sh implementations (Termux's
	// default sh prints the literal escape sequence), and a `cat` of a fixed
	// file is unambiguous.
	payload := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(payload, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bad-bytes")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat "+payload+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := runClipboardCmd(context.Background(), bin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit_code: %d", res.ExitCode)
	}
	if len(res.RawStdout) != 2 || res.RawStdout[0] != 0xff {
		t.Errorf("raw_stdout: %x", res.RawStdout)
	}
	if !strings.Contains(res.Stdout, "�") {
		t.Errorf("expected U+FFFD replacement in Stdout, got %q", res.Stdout)
	}
}

func TestCappedBufferTruncates(t *testing.T) {
	cb := &cappedBuffer{max: 5}
	n, _ := cb.Write([]byte("hello world"))
	if n != len("hello world") {
		t.Errorf("Write should report all bytes consumed, got %d", n)
	}
	if !cb.truncated {
		t.Error("expected truncated=true")
	}
	if string(cb.Bytes()) != "hello" {
		t.Errorf("buffer: %q", cb.Bytes())
	}
}

func TestSafeUTF8(t *testing.T) {
	if got := safeUTF8([]byte("plain ascii")); got != "plain ascii" {
		t.Errorf("ascii: %q", got)
	}
	bad := []byte{0x68, 0xff, 0x69} // h <0xff> i
	got := safeUTF8(bad)
	if !strings.Contains(got, "h") || !strings.Contains(got, "i") {
		t.Errorf("expected h…i with replacement, got %q", got)
	}
	if !strings.Contains(got, "�") {
		t.Errorf("expected replacement char, got %q", got)
	}
}
