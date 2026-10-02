package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// sessionIDFromCookie extracts the chat-session cookie value
// from a response so the test can round-trip the session id
// across requests.
func sessionIDFromCookie(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "ragabast_chat_session" {
			return c.Value
		}
	}
	return ""
}

// openSession does GET / and returns the session id (cookie)
// and the recorder (for assertions on the body).
func openSession(t *testing.T, s *Server) (string, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "GET / must return 200")
	id := sessionIDFromCookie(t, w)
	require.NotEmpty(t, id, "GET / must set the ragabast_chat_session cookie")
	return id, w
}

// postMessage sends a /chat/message POST with the given form body
// and the session cookie. Returns the recorder for assertions.
func postMessage(t *testing.T, s *Server, sessionID, formBody string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/message",
		strings.NewReader(formBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "ragabast_chat_session", Value: sessionID})
	s.router.ServeHTTP(w, req)
	return w
}

// getChatPage does GET / with the session cookie and returns the
// recorder for body assertions.
func getChatPage(t *testing.T, s *Server, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_chat_session", Value: sessionID})
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "GET / must return 200")
	return w
}

// isChecked scans the rendered form body for a checkbox with the
// given value attribute and returns whether it's checked. Anchors
// on the value= attribute and looks for "checked" before the
// closing ">" of that input element. Robust to template
// whitespace between the two.
func isChecked(body, valueAttr string) bool {
	idx := strings.Index(body, `value="`+valueAttr+`"`)
	if idx == -1 {
		return false
	}
	// Find the closing ">" after the value= attribute; everything
	// inside is the rest of the input element.
	closeIdx := strings.Index(body[idx:], ">")
	if closeIdx == -1 {
		return false
	}
	inner := body[idx : idx+closeIdx]
	return strings.Contains(inner, "checked")
}

// checkboxCount returns how many checkboxes in the form body
// are checked. Useful for asserting "only gitlab is checked, not
// docbuilder" without depending on whitespace-sensitive substring
// matching.
func checkboxCount(body string) int {
	count := 0
	for i := 0; i < len(body); i++ {
		if strings.HasPrefix(body[i:], "<input") {
			end := strings.Index(body[i:], ">")
			if end == -1 {
				break
			}
			inner := body[i : i+end]
			if strings.Contains(inner, `type="checkbox"`) && strings.Contains(inner, "checked") {
				count++
			}
			i += end
		}
	}
	return count
}

// TestChatForm_StickyDefault_RoundTripsThroughPOSTThenGET pins the
// headline behavior: a POST that submits source_kinds=gitlab
// persists the selection; a subsequent GET renders the form
// with the gitlab checkbox pre-checked. The form's default
// state is "every box checked" (no selection = all sources)
// for the first GET; the post-selection GET pre-fills with the
// saved kind.
func TestChatForm_StickyDefault_RoundTripsThroughPOSTThenGET(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{queryDebug: &service.QueryDebugInfo{Results: nil}}
	s := NewServer(cfg, fake)

	// Open session via GET.
	sessionID, get1 := openSession(t, s)
	require.Equal(t, 2, checkboxCount(get1.Body.String()),
		"first GET must check every box (no default = all sources)")

	// POST: select gitlab.
	post := postMessage(t, s, sessionID,
		"message=hello&session_id="+sessionID+"&source_kinds=gitlab")
	require.Equal(t, http.StatusOK, post.Code)

	// Subsequent GET: form pre-fills with gitlab checked.
	get2 := getChatPage(t, s, sessionID)
	body := get2.Body.String()
	require.True(t, isChecked(body, "gitlab"),
		"second GET must have gitlab box checked (sticky default)")
	require.False(t, isChecked(body, "docbuilder"),
		"second GET must NOT have docbuilder box checked")
}

// TestChatForm_EmptySubmissionDoesNotClearDefault pins the
// "per-question override, sticky default" semantics: a POST
// that submits source_kinds=gitlab sets the default. A
// follow-up POST that omits the field does NOT clear the
// stored default. The session's default survives so the
// operator can still untick a single box next turn and get
// back to "all sources" + their original sticky default.
func TestChatForm_EmptySubmissionDoesNotClearDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{queryDebug: &service.QueryDebugInfo{Results: nil}}
	s := NewServer(cfg, fake)

	sessionID, _ := openSession(t, s)

	// Establish default: pick gitlab.
	post1 := postMessage(t, s, sessionID,
		"message=hello&session_id="+sessionID+"&source_kinds=gitlab")
	require.Equal(t, http.StatusOK, post1.Code)

	// Empty submission: no source_kinds at all.
	post2 := postMessage(t, s, sessionID,
		"message=hi&session_id="+sessionID)
	require.Equal(t, http.StatusOK, post2.Code)

	// GET: pre-fill still shows gitlab.
	get := getChatPage(t, s, sessionID)
	require.True(t, isChecked(get.Body.String(), "gitlab"),
		"empty POST must NOT clear the stored gitlab default")
}

// TestChatForm_ChangingSelectionUpdatesDefault pins the "override
// per question" side: a follow-up POST that picks docbuilder
// must update the default to docbuilder (and the form must
// reflect that on the next GET).
func TestChatForm_ChangingSelectionUpdatesDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{queryDebug: &service.QueryDebugInfo{Results: nil}}
	s := NewServer(cfg, fake)

	sessionID, _ := openSession(t, s)

	post1 := postMessage(t, s, sessionID,
		"message=q1&session_id="+sessionID+"&source_kinds=gitlab")
	require.Equal(t, http.StatusOK, post1.Code)

	post2 := postMessage(t, s, sessionID,
		"message=q2&session_id="+sessionID+"&source_kinds=docbuilder")
	require.Equal(t, http.StatusOK, post2.Code)

	// GET: docbuilder checked, gitlab NOT checked.
	get := getChatPage(t, s, sessionID)
	body := get.Body.String()
	require.True(t, isChecked(body, "docbuilder"),
		"docbuilder must be checked after switching")
	require.False(t, isChecked(body, "gitlab"),
		"gitlab must NOT be checked after switching to docbuilder")
}

// TestChatForm_ClearWipesSourceKindDefault pins the "private mode"
// / /chat/clear flow: clearing a session drops BOTH the
// conversation history AND the source-kind default. After
// clear, a subsequent fresh session (no cookie) shows all boxes
// checked — the "no default" / "all sources" UX. This is the
// same as a first visit; the spec contract is that /chat/clear
// removes the per-session source-kind default entirely, not
// that it switches to "no boxes checked" (which would be
// indistinguishable from a never-pressed preference).
func TestChatForm_ClearWipesSourceKindDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{queryDebug: &service.QueryDebugInfo{Results: nil}}
	s := NewServer(cfg, fake)

	sessionID, _ := openSession(t, s)

	// Establish a default.
	post := postMessage(t, s, sessionID,
		"message=q&session_id="+sessionID+"&source_kinds=gitlab")
	require.Equal(t, http.StatusOK, post.Code)

	// /chat/clear wipes everything.
	clearW := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/clear",
		strings.NewReader("csrf_token=x&session_id="+sessionID))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: "ragabast_chat_session", Value: sessionID})
	s.router.ServeHTTP(clearW, req2)
	require.Equal(t, http.StatusSeeOther, clearW.Code,
		"/chat/clear must redirect to /")

	// Fake state confirms the wipe.
	require.Empty(t, fake.chatSessionKinds,
		"after /chat/clear the session's source-kind default must be gone")

	// Fresh session: no cookie → all boxes checked.
	get := openSessionAsFresh(t, s)
	require.Equal(t, 2, checkboxCount(get.Body.String()),
		"after /chat/clear a fresh session must render with all boxes checked (no default)")
}

// openSessionAsFresh does GET / with no cookie, simulating a
// brand-new visitor (the state after /chat/clear wipes the
// session cookie and the operator lands on / again).
func openSessionAsFresh(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "GET / must return 200")
	return w
}

// TestChatForm_FirstGETNoCheckedBoxes pins the empty-default
// UX: a fresh visitor (no session) sees all boxes checked (no
// selection = "all sources"). Pre-checking no boxes on first
// visit would be ambiguous between "I selected nothing" and
// "I haven't been here before"; pre-checking all is unambiguous
// (no filter = all sources).
func TestChatForm_FirstGETAllChecked(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{}
	s := NewServer(cfg, fake)

	_, get := openSession(t, s)
	body := get.Body.String()
	require.Contains(t, body, "source-kind-option",
		"the form must render the multi-select")
	// Both known kinds must be checked on first visit.
	require.True(t, isChecked(body, "gitlab"))
	require.True(t, isChecked(body, "docbuilder"))
}
