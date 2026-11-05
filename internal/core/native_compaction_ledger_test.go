package core

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNativeCompactionLedgerRetainsPathsAndCommandArguments(t *testing.T) {
	var ledger semanticContextLedger
	ledger.add([]nativeMessage{
		{Role: "assistant", ToolCalls: []nativeToolCall{
			nativeCall("read-a", "read_file", `{"path":"src/config.go"}`),
			nativeCall("read-b", "read_file", `{"path":"tests/config.go"}`),
			nativeCall("test", "run_command", `{"command":"go","args":["test","./internal/core"]}`),
			nativeCall("edit", "edit_file", `{"path":"src/config.go","old_string":"missing","new_string":"fixed"}`),
		}},
		{Role: "tool", ToolCallID: "edit", Content: "Tool failed: old_string was not found"},
	})
	got := ledger.format(4096)
	for _, want := range []string{"src/config.go", "tests/config.go", "test", "./internal/core", "old_string was not found"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compaction lost %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Files modified:") {
		t.Fatal("an attempted edit was reported as a confirmed modification")
	}
}

func TestNativeCompactionLedgerKeepsRecentCorrectionsAndAllCategories(t *testing.T) {
	var ledger semanticContextLedger
	ledger.add([]nativeMessage{{Role: "user", Content: "ORIGINAL-GOAL: improve the project"}})
	for i := range 100 {
		ledger.add([]nativeMessage{{Role: "user", Content: fmt.Sprintf("Earlier instruction %d: %s", i, strings.Repeat("detail ", 20))}})
	}
	ledger.add([]nativeMessage{
		{Role: "user", Content: "LATEST-CORRECTION: keep compatibility"},
		{Role: "assistant", Content: "LATEST-PROGRESS: API migration is complete"},
		{Role: "tool", Content: "Tool failed: LATEST-FAILURE: package build failed"},
	})
	got := ledger.format(800)
	for _, want := range []string{"ORIGINAL-GOAL", "LATEST-CORRECTION", "LATEST-PROGRESS", "LATEST-FAILURE"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compaction lost %q: %s", want, got)
		}
	}
	if len(got) > 800 || !utf8.ValidString(got) {
		t.Fatal("invalid bounded compaction receipt")
	}
	if got := ledger.format(0); got != "" {
		t.Fatal("zero receipt budget produced unbounded context")
	}
}

func TestNativeCompactionLedgerDoesNotTreatSourceTextAsFailure(t *testing.T) {
	var ledger semanticContextLedger
	ledger.add([]nativeMessage{{Role: "tool", Content: "func HandleError() error { return nil }"}})
	if got := ledger.format(1000); strings.Contains(got, "Errors noted") {
		t.Fatalf("source code was mistaken for a failed tool: %s", got)
	}
}
