package web

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/ragabast/internal/vector"
)

// defaultChatTopK is the number of retrieved chunks the chat form uses when
// the client does not pass an explicit top_k. Each chunk is augmented with
// its parent section header, so 5 results is a comfortable default.
const defaultChatTopK = 5

// maxChatTopK caps the chat form's top_k to prevent a runaway client from
// pulling the entire vector DB into the LLM context.
const maxChatTopK = 50

// chatTopK reads an optional 'top_k' form value, falling back to
// defaultChatTopK. Out-of-range or unparseable values are clamped.
func chatTopK(r *http.Request) int {
	raw := strings.TrimSpace(r.FormValue("top_k"))
	if raw == "" {
		return defaultChatTopK
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultChatTopK
	}
	if v > maxChatTopK {
		return maxChatTopK
	}
	return v
}

// parseSourceKindsForm reads the chat form's optional
// `source_kinds` field (a comma-separated list of kinds) and
// returns the recognized entries. Empty input or input that
// contains no recognized kinds returns nil, which the caller
// interprets as "no filter / all sources".
//
// Why drop unknown kinds rather than error: a future SourceKind
// (e.g. "redmine") arriving via the form before this handler
// knows about it would otherwise 400 the chat. Silently dropping
// keeps the chat endpoint available and means the worst case for
// a new kind is "operator doesn't get the new kind in the filter
// until we ship code" — a one-deploy delay, not an outage.
//
// The wire values are the SourceKind constants verbatim
// ("docbuilder", "gitlab"); matching is case-sensitive to keep
// the contract simple.
func parseSourceKindsForm(r *http.Request) []models.SourceKind {
	raw := strings.TrimSpace(r.FormValue("source_kinds"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]models.SourceKind, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k := models.SourceKind(p)
		if !k.IsValid() {
			continue
		}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	// Issue #22 — pin a session id across page reloads.
	// First-time callers get a UUID via Set-Cookie; the
	// form embeds the same id in a hidden field so the
	// subsequent POST round-trips it back to the server.
	sessionID, shouldSet := ensureChatSessionID(r)
	if shouldSet {
		setChatSessionCookie(w, sessionID)
	}
	s.renderTemplate(w, "chat.html", map[string]any{
		"Title":     "RAGabast - Chat",
		"CsrfToken": CsrfTokenFromContext(r.Context()),
		"SessionID": sessionID,
		"Header":    s.pageHeaderFromContext(r),
	})
}

// handleChatClear implements POST /chat/clear: drops
// the session history AND the cookie so the next
// /chat/message starts fresh. Used by the "private
// mode" UI affordance.
func (s *Server) handleChatClear(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	s.service.ClearChatSession(sessionID)
	clearChatSessionCookie(w)
	// Redirect back to the chat page so the browser
	// re-renders with a fresh session-id cookie set on
	// the response.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	msg := strings.TrimSpace(r.FormValue("message"))
	if msg == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	// Issue #22 — read prior context from the session
	// store (empty for first-time callers). Threads the
	// history into the LLM call so follow-up questions
	// ("tell me more about that") carry the prior
	// exchange. Operators with no session_id opt out of
	// persistence via the empty-string no-op path.
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	history := s.service.ChatSessionHistory(sessionID)

	answer, info, err := s.service.QueryDebugWithOptions(r.Context(), msg, chatTopK(r), service.LLMOptions{
		History: history,
		// Per-question source-kind filter (Stage 2.1 will
		// add the chat form UI; today a curl operator can
		// pass source_kinds=gitlab,docbuilder to scope the
		// retrieval). Empty result means "no filter / all
		// sources" — matches the post-filter's empty-means-
		// no-op semantics.
		Filters: vector.SearchFilters{
			SourceKinds: parseSourceKindsForm(r),
		},
	})
	if err != nil {
		internalError(w, r, "query", err)
		return
	}

	// Record the new exchange (user + assistant) so the
	// NEXT request in this session sees it. The empty
	// sessionID no-op in AppendChatTurn keeps single-shot
	// callers (no cookie, no form field) stateless.
	if sessionID != "" {
		_ = s.service.AppendChatTurn(
			r.Context(), sessionID,
			service.ChatMessage{Role: "user", Content: msg},
			service.ChatMessage{Role: "assistant", Content: answer},
		)
	}

	var sources []models.SearchResult
	if info != nil {
		sources = info.Results
	}

	// Strip the model's own native thinking tokens
	// (<think>...</think> and the Qwen-style [think]...[/think]
	// variant). Some model families (Qwen, DeepSeek, ...) emit
	// their own reasoning tokens as a parallel channel to the
	// ragabast-directed <scratchpad>...</scratchpad> format.
	// Without this stripper, the user's visible reply includes
	// the model's "thinking out loud" preamble even when the
	// model also complies with our scratchpad directive. This is
	// the most common source of leaked preamble that the user
	// has been seeing.
	answer = service.StripThinkTags(answer)

	// Strip the <scratchpad>...</scratchpad> block the prompt
	// asks the LLM to wrap its reasoning in. Deterministic — no
	// heuristic matching of "Let me check…" style starters. If
	// the model forgot the format, this is a no-op and the next
	// step (StripLeadingThinking) acts as the heuristic backstop.
	answer = service.StripScratchpad(answer)

	// Heuristic backstop for models that didn't follow the
	// scratchpad format: strip leading "Let me check…" /
	// "Wait, actually…" / "I need to look at…" / "Hmm, that's
	// interesting…" paragraphs that some models emit BEFORE the
	// actual answer. Stops as soon as it finds a paragraph whose
	// first line doesn't look like thinking, so a real answer
	// that happens to begin with one of these markers is
	// preserved.
	answer = service.StripLeadingThinking(answer)

	// Strip a trailing "Links:" section the model emits despite
	// the prompt telling it not to. Inline [src:N] citations
	// cover the legitimate cases; the trailing section often
	// contains placeholder URLs ("https://docs.example.com")
	// that the model invented.
	answer = service.StripTrailingLinksSection(answer)

	// Inline [src:N] markers → markdown links to source N's URL.
	// Must run BEFORE the markdown renderer so the resulting
	// [title](url) syntax gets converted to <a> tags by the
	// markdown pipeline. The function is a no-op on replies that
	// don't use the marker syntax (common for replies that don't
	// cite anything).
	answerWithInlineLinks := service.InlineSourceLinks(answer, sources)

	// Render the (now inline-linked) LLM reply to HTML.
	//
	// Trust model: the LLM reply is untrusted text. Anything
	// that lands in {{ .AnswerHTML }} on the page is wrapped
	// as template.HTML (so html/template does not double-escape
	// the legitimate markdown tags we just rendered). Wrapping
	// as template.HTML is a security smell — every other field
	// in the template goes through html/template's auto-escape,
	// so this one field becomes "trusted by convention". To
	// keep that trust explicit, the pipeline below ends with a
	// second bluemonday UGCPolicy pass (sanitizeForChatHTML)
	// that strips anything the markdown renderer or
	// InlineSourceLinks introduced that the first sanitizer
	// pass missed. The markdown renderer already runs bluemonday
	// once (see internal/web/markdown.go); this second pass is
	// defense in depth, not a substitute.
	answerHTML, mdErr := renderChatMarkdownToSafeHTML(answerWithInlineLinks)
	if mdErr != nil {
		// Fall back to escaped plaintext so the user still sees
		// the assistant's reply if markdown rendering blows up.
		// The string is HTML-escaped so it is safe to wrap as
		// template.HTML below.
		answerHTML = template.HTMLEscapeString(answerWithInlineLinks)
	}
	answerHTML = sanitizeForChatHTML(answerHTML)

	s.renderTemplate(w, "chat_message.html", map[string]any{
		"User":          msg,
		"Answer":        answerWithInlineLinks,
		"AnswerHTML":    template.HTML(answerHTML),
		"Sources":       sources,
		"SourcesByKind": sourcesByKind(sources),
	})
}

// kindGroup is one section of the chat sources panel: every
// source with the same SourceKind, in input order (which the
// service layer populates as similarity-descending, so the most
// relevant cited source for a kind is at the top of its group).
type kindGroup struct {
	Kind    models.SourceKind
	Sources []models.SearchResult
}

// sourcesByKind groups sources by SourceKind for the chat
// sources panel. Returns a slice (not a map) so the template
// iterates in a stable order; map iteration in Go is randomized,
// which would surface as flaky group-header ordering in the
// rendered output.
//
// Within each group, sources preserve their input order — the
// service layer already returns them sorted by similarity, so
// the most-relevant-cited-source lands at the top of its kind's
// section.
//
// Group order is fixed: docbuilder first (the legacy default,
// what operators saw for the entire pre-multi-source era),
// then gitlab, then any future kinds in declaration order
// (SourceUnknown included as a fallback so legacy chunks
// surface; the template suppresses the group header for empty
// SourceUnknown groups, so an empty legacy chunk shows as
// nothing rather than a misleading badge).
func sourcesByKind(sources []models.SearchResult) []kindGroup {
	kindOrder := []models.SourceKind{
		models.SourceDocbuilder,
		models.SourceGitLab,
		models.SourceUnknown,
	}
	grouped := make(map[models.SourceKind][]models.SearchResult, len(kindOrder))
	for _, s := range sources {
		k := s.SourceKind
		if k == "" {
			k = models.SourceUnknown
		}
		grouped[k] = append(grouped[k], s)
	}
	out := make([]kindGroup, 0, len(kindOrder))
	for _, k := range kindOrder {
		if entries := grouped[k]; len(entries) > 0 {
			out = append(out, kindGroup{Kind: k, Sources: entries})
		}
	}
	return out
}
