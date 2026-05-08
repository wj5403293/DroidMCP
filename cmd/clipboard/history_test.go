package main

import (
	"bytes"
	"testing"
)

func TestHistoryEntryCap(t *testing.T) {
	h := &clipboardHistory{maxEntries: 3, maxBytes: 1 << 20}
	for i := 0; i < 5; i++ {
		h.Push([]byte{byte('a' + i)}, "text")
	}
	snap := h.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", len(snap))
	}
	if string(snap[0].Content) != "c" || string(snap[2].Content) != "e" {
		t.Errorf("eviction order wrong: %+v", snap)
	}
}

func TestHistoryByteCap(t *testing.T) {
	h := &clipboardHistory{maxEntries: 100, maxBytes: 10}
	h.Push(bytes.Repeat([]byte("a"), 4), "text")
	h.Push(bytes.Repeat([]byte("b"), 4), "text")
	h.Push(bytes.Repeat([]byte("c"), 4), "text") // total 12 > 10 → oldest evicted
	snap := h.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries after byte-cap eviction, got %d", len(snap))
	}
	if string(snap[0].Content) != "bbbb" || string(snap[1].Content) != "cccc" {
		t.Errorf("unexpected content after eviction: %+v", snap)
	}
}

func TestHistoryEntryTruncation(t *testing.T) {
	h := &clipboardHistory{maxEntries: 4, maxBytes: 1 << 30}
	big := bytes.Repeat([]byte("x"), int(maxStoredEntryBytes)+100)
	h.Push(big, "text")
	snap := h.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snap))
	}
	if !snap[0].Truncated {
		t.Error("expected truncated=true")
	}
	if int64(len(snap[0].Content)) != maxStoredEntryBytes {
		t.Errorf("expected content len = maxStoredEntryBytes (%d), got %d", maxStoredEntryBytes, len(snap[0].Content))
	}
	if snap[0].BytesLen != len(big) {
		t.Errorf("BytesLen should retain original size: got %d, want %d", snap[0].BytesLen, len(big))
	}
}

func TestHistoryClear(t *testing.T) {
	h := &clipboardHistory{maxEntries: 4, maxBytes: 1 << 20}
	h.Push([]byte("hello"), "text")
	h.Push([]byte("world"), "text")
	h.Clear()
	if snap := h.Snapshot(); len(snap) != 0 {
		t.Errorf("expected empty snapshot after Clear, got %d entries", len(snap))
	}
	if h.curBytes != 0 {
		t.Errorf("expected curBytes=0, got %d", h.curBytes)
	}
}

func TestIntFromEnvClampsAndDefaults(t *testing.T) {
	t.Setenv("DROIDMCP_TEST_HIST_INT", "")
	if got := intFromEnv("DROIDMCP_TEST_HIST_INT", 7, 1, 100); got != 7 {
		t.Errorf("empty -> default: got %d", got)
	}
	t.Setenv("DROIDMCP_TEST_HIST_INT", "garbage")
	if got := intFromEnv("DROIDMCP_TEST_HIST_INT", 7, 1, 100); got != 7 {
		t.Errorf("garbage -> default: got %d", got)
	}
	t.Setenv("DROIDMCP_TEST_HIST_INT", "0")
	if got := intFromEnv("DROIDMCP_TEST_HIST_INT", 7, 1, 100); got != 7 {
		t.Errorf("below min -> default: got %d", got)
	}
	t.Setenv("DROIDMCP_TEST_HIST_INT", "9999")
	if got := intFromEnv("DROIDMCP_TEST_HIST_INT", 7, 1, 100); got != 100 {
		t.Errorf("above max -> clamp: got %d", got)
	}
	t.Setenv("DROIDMCP_TEST_HIST_INT", "42")
	if got := intFromEnv("DROIDMCP_TEST_HIST_INT", 7, 1, 100); got != 42 {
		t.Errorf("valid: got %d", got)
	}
}
