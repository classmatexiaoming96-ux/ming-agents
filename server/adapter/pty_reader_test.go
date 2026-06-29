package adapter

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPTYReaderSubscribeReceivesRawOutputWhileSnapshotIsNormalized(t *testing.T) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	reader := NewPTYReader(readFile)
	rawCh, unsubscribe := reader.Subscribe()
	defer unsubscribe()
	go reader.ReadLoop()
	defer reader.Close()

	raw := "\x1b[31mred\x1b[0m\n"
	if _, err := writeFile.Write([]byte(raw)); err != nil {
		t.Fatalf("write pipe: %v", err)
	}

	select {
	case got := <-rawCh:
		if string(got) != raw {
			t.Fatalf("raw subscriber got %q, want %q", string(got), raw)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for raw subscriber output")
	}

	normalized, _ := reader.Snapshot()
	if !strings.Contains(normalized, "red\n") {
		t.Fatalf("normalized snapshot = %q, want stripped text", normalized)
	}
	if strings.Contains(normalized, "\x1b[31m") {
		t.Fatalf("normalized snapshot retained ANSI sequence: %q", normalized)
	}
}

func TestPTYReaderUnsubscribeStopsRawDelivery(t *testing.T) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	reader := NewPTYReader(readFile)
	rawCh, unsubscribe := reader.Subscribe()
	go reader.ReadLoop()
	defer reader.Close()

	unsubscribe()
	if _, ok := <-rawCh; ok {
		t.Fatal("raw subscription channel remains open after unsubscribe")
	}

	if _, err := writeFile.Write([]byte("after unsubscribe\n")); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
}
