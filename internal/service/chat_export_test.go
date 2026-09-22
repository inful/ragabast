package service

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExportChatTranscript_EmptySession pins the empty
// case: a session that has never been written to must
// produce an empty string (no headings, no body). This
// is what the HTTP handler returns to set a
// "no-transcript-yet" 404 — without it, we'd return
// an empty .md file and confuse the user.
func TestExportChatTranscript_EmptySession(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	md := svc.ExportChatTranscript("never-existed")
	require.Empty(t, md,
		"unknown session must return empty string — no headings, no body")
}

// TestExportChatTranscript_FormatsTurnsAsHeadings pins
// the markdown contract: each turn becomes a `## User`
// or `## Assistant` heading followed by the message
// body. Heading-level 2 keeps the transcript readable
// in any markdown viewer (H1 is reserved for the
// transcript title).
func TestExportChatTranscript_FormatsTurnsAsHeadings(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	_ = svc.AppendChatTurn(context.Background(), "s",
		ChatMessage{Role: "user", Content: "What is ragabast?"},
		ChatMessage{Role: "assistant", Content: "A markdown chunker + RAG server."},
	)

	md := svc.ExportChatTranscript("s")

	require.Contains(t, md, "## User",
		"user turns must use `## User` heading (H2, not H3 — readable in viewers)")
	require.Contains(t, md, "What is ragabast?",
		"body content must follow the heading")
	require.Contains(t, md, "## Assistant",
		"assistant turns must use `## Assistant` heading")
	require.Contains(t, md, "A markdown chunker + RAG server.",
		"assistant body must follow the heading")

	// User heading must appear before its body, which
	// must appear before Assistant heading. Pins the
	// FTS-style round-trip — the transcript must read
	// top-to-bottom in chronological order.
	userIdx := strings.Index(md, "## User")
	bodyIdx := strings.Index(md, "What is ragabast?")
	asstIdx := strings.Index(md, "## Assistant")
	require.Greater(t, bodyIdx, userIdx,
		"body must follow the user heading")
	require.Greater(t, asstIdx, bodyIdx,
		"assistant heading must follow the user body")
}

// TestExportChatTranscript_RoleHeaderCasing pins the
// casing convention. `## user` reads as a typo; we use
// `## User` everywhere because that's what every chat
// UI uses, and consistency with operator expectations
// matters more than consistency with the role string
// the model emits.
func TestExportChatTranscript_RoleHeaderCasing(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	// Use the canonical role names — the export
	// function should accept them as-is, not normalize.
	_ = svc.AppendChatTurn(context.Background(), "s",
		ChatMessage{Role: "user", Content: "hi"},
		ChatMessage{Role: "assistant", Content: "hello"},
	)

	md := svc.ExportChatTranscript("s")
	require.Contains(t, md, "## User")
	require.Contains(t, md, "## Assistant")
	require.NotContains(t, md, "## user")
	require.NotContains(t, md, "## assistant")
}

// TestExportChatTranscript_MultipleTurnsAreAllIncluded
// pins that the export contains EVERY turn, not just
// the most recent. This is the user's "save my
// conversation" feature — silently dropping older
// turns would be a serious data-loss bug.
func TestExportChatTranscript_MultipleTurnsAreAllIncluded(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	for range 3 {
		err := svc.AppendChatTurn(context.Background(), "s",
			ChatMessage{Role: "user", Content: "q"},
			ChatMessage{Role: "assistant", Content: "a"})
		require.NoError(t, err)
	}

	md := svc.ExportChatTranscript("s")
	require.Equal(t, 3, strings.Count(md, "## User"),
		"all 3 user turns must be exported (data-loss guard)")
	require.Equal(t, 3, strings.Count(md, "## Assistant"),
		"all 3 assistant turns must be exported")
}

// TestExportChatTranscript_RespectsMaxTurns pins the
// interaction with the FIFO trim. The export must
// show exactly the same messages that the LLM sees —
// if the store trims to MaxTurns, the export must
// reflect that, not pull a "complete" history from
// somewhere else.
func TestExportChatTranscript_RespectsMaxTurns(t *testing.T) {
	t.Parallel()

	// Use a 2-turn store directly so the FIFO trim is
	// observable. The newServiceForChatTests helper uses
	// MaxTurns=20 (the production default) which would
	// make this test trivially pass at 5 turns.
	svc := &Service{
		chatSessions: NewChatSessionStore(ChatSessionStoreConfig{MaxTurns: 2}),
	}
	// 3 turns of distinct content + 1 marker = 4 total.
	// MaxTurns=2 keeps only the LAST 2 turns (the
	// marker + the 3rd-old turn). The first two old
	// turns are FIFO-trimmed.
	for i := 1; i <= 3; i++ {
		content := "turn-" + strconv.Itoa(i) + "-content"
		err := svc.AppendChatTurn(context.Background(), "s",
			ChatMessage{Role: "user", Content: content + "-q"},
			ChatMessage{Role: "assistant", Content: content + "-a"})
		require.NoError(t, err)
	}
	// Now the latest turn, which must survive.
	_ = svc.AppendChatTurn(context.Background(), "s",
		ChatMessage{Role: "user", Content: "marker-q"},
		ChatMessage{Role: "assistant", Content: "marker-a"},
	)
	// Sanity check: only 2 turns survive (MaxTurns=2),
	// so 4 messages total.
	hist := svc.ChatSessionHistory("s")
	require.Len(t, hist, 4,
		"sanity: MaxTurns=2 keeps exactly 2 turns × 2 messages")

	md := svc.ExportChatTranscript("s")
	require.Contains(t, md, "marker-q",
		"latest turn must be in the export")
	// The export reflects the FIFO-trimmed history, so
	// only the last 2 of 4 turns should remain.
	require.NotContains(t, md, "turn-1-content",
		"oldest turn must be trimmed — would inflate the saved file")
	require.NotContains(t, md, "turn-2-content",
		"second-oldest turn must be trimmed too")
	require.Contains(t, md, "turn-3-content",
		"most-recent old turn must survive (one of the 2 retained)")
	require.Contains(t, md, "marker-q",
		"marker turn must survive")
}
