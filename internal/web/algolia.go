package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// Algolia DocSearch compatibility layer.
//
// Algolia's DocSearch JavaScript client (docsearch.js v3) talks to
// Algolia's Search API with two custom headers and a fixed
// response envelope. This file implements the subset of that
// contract ragabast can serve from its own index:
//
//	POST /1/indexes/{indexName}/queries   multi-query (what docsearch.js calls)
//	POST /1/indexes/{indexName}/query     single-index search
//
// The Algolia client posts the query string as JSON but labels it
// `application/x-www-form-urlencoded`, so the body is parsed as
// JSON regardless of the Content-Type header.
//
// Credentials: the client sends the API key in the x-algolia-api-key
// header instead of Authorization. The auth middleware accepts that
// header as an alternative bearer credential, so an operator reuses
// the existing server.auth_token / server.auth_tokens list.

const (
	// algoliaAPIKeyHeader is the header Algolia's clients use to
	// authenticate. The auth middleware treats its value as an
	// alternative bearer credential.
	algoliaAPIKeyHeader = "X-Algolia-API-Key"

	// algoliaAppIDHeader identifies the calling application.
	// ragabast ignores the value, but the header must be allowed
	// through CORS or the browser preflight fails.
	algoliaAppIDHeader = "X-Algolia-Application-Id"

	// algoliaAPIKeyParam is the query-string form of the API key.
	// The Algolia client sends it lowercased when it runs with
	// authMode=withinQueryParameters. Query-string keys are
	// case-sensitive, so it cannot be the header constant.
	algoliaAPIKeyParam = "x-algolia-api-key"

	// algoliaMaxBodyBytes caps the request body. The Algolia
	// client sends a small JSON envelope; 1 MiB is generous.
	algoliaMaxBodyBytes = 1 << 20

	// algoliaMaxHits caps how many records one query pulls from
	// the index. It matches the vector layer's own clamp
	// (min(collection count, 1000)) so a deep page does not ask
	// for more than the backend can return.
	algoliaMaxHits = 1000

	// algoliaDefaultHitsPerPage mirrors Algolia's default.
	algoliaDefaultHitsPerPage = 20

	// algoliaDefaultSnippetSize mirrors Algolia's default snippet
	// window, in words.
	algoliaDefaultSnippetSize = 10

	// algoliaDefaultIndexName is used when a request omits the
	// index name. Every index name maps to the same ragabast
	// corpus — ragabast has a single knowledge base.
	algoliaDefaultIndexName = "ragabast"
)

// algoliaParams holds the search parameters of one Algolia query,
// normalized from either the multi-query `params` query string or
// the single-index JSON body.
type algoliaParams struct {
	Query                 string
	HitsPerPage           int
	Page                  int
	AttributesToRetrieve  []string
	AttributesToHighlight []string
	AttributesToSnippet   []string
	HighlightPreTag       string
	HighlightPostTag      string
	SnippetEllipsisText   string

	// RawParams is the query string the client sent, echoed back
	// in the response the way Algolia echoes it.
	RawParams string
}

// algoliaResult is the Algolia search response envelope.
type algoliaResult struct {
	Hits             []map[string]any `json:"hits"`
	NbHits           int              `json:"nbHits"`
	Page             int              `json:"page"`
	NbPages          int              `json:"nbPages"`
	HitsPerPage      int              `json:"hitsPerPage"`
	ProcessingTimeMS int              `json:"processingTimeMS"`
	ExhaustiveNbHits bool             `json:"exhaustiveNbHits"`
	Query            string           `json:"query"`
	Params           string           `json:"params"`
	Index            string           `json:"index"`
}

// algoliaMultiRequest is the body of POST /1/indexes/*/queries.
type algoliaMultiRequest struct {
	Requests []algoliaRequestSpec `json:"requests"`
}

// errAlgoliaBadRequest marks a malformed request so the handler
// can answer 400 instead of 500.
var errAlgoliaBadRequest = errors.New("invalid algolia request")

// algoliaRequestSpec is one entry of the multi-query body. The
// Algolia client puts the query next to `params` rather than
// inside it, so both places are read.
type algoliaRequestSpec struct {
	IndexName string `json:"indexName"`
	Query     string `json:"query"`
	Params    string `json:"params"`
}

// algoliaSingleRequest is the body of POST /1/indexes/{index}/query.
type algoliaSingleRequest struct {
	Query                 string   `json:"query"`
	HitsPerPage           *int     `json:"hitsPerPage,omitempty"`
	Page                  *int     `json:"page,omitempty"`
	AttributesToRetrieve  []string `json:"attributesToRetrieve,omitempty"`
	AttributesToHighlight []string `json:"attributesToHighlight,omitempty"`
	AttributesToSnippet   []string `json:"attributesToSnippet,omitempty"`
	HighlightPreTag       string   `json:"highlightPreTag,omitempty"`
	HighlightPostTag      string   `json:"highlightPostTag,omitempty"`
	SnippetEllipsisText   string   `json:"snippetEllipsisText,omitempty"`
}

// algoliaNormalizeAPIKey copies an API key presented in the query
// string into the header.
//
// The Algolia client supports authMode=withinQueryParameters, which
// puts the key in the URL instead of the header. Normalizing here
// means the auth, CSRF and audit layers only ever look at the
// header. The access logger redacts the query-string form
// separately (see redactedQueryKeys).
func algoliaNormalizeAPIKey(r *http.Request) {
	if r.Header.Get(algoliaAPIKeyHeader) != "" {
		return
	}
	if key := r.URL.Query().Get(algoliaAPIKeyParam); key != "" {
		r.Header.Set(algoliaAPIKeyHeader, key)
	}
}

// algoliaDocSearchHandler serves the Algolia DocSearch search
// contract on top of the ragabast index. Both the multi-query and
// the single-index paths land here; the trailing path segment
// selects the response shape.
func algoliaDocSearchHandler(svc serviceAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, algoliaMaxBodyBytes))
		if err != nil {
			algoliaWriteError(w, http.StatusBadRequest, "cannot read request body")
			return
		}

		indexName := chi.URLParam(r, "indexName")
		if indexName == "" {
			indexName = algoliaDefaultIndexName
		}

		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/queries") {
			results, queryErr := algoliaMultiQuery(r.Context(), svc, body)
			if queryErr != nil {
				algoliaWriteError(w, algoliaErrorStatus(queryErr), queryErr.Error())
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
			return
		}

		result, err := algoliaSingleQuery(r.Context(), svc, indexName, body)
		if err != nil {
			algoliaWriteError(w, algoliaErrorStatus(err), err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}

// algoliaErrorStatus maps a failure onto an HTTP status. A
// malformed request is the caller's fault (400); anything else is
// a backend failure (500).
func algoliaErrorStatus(err error) int {
	if errors.Is(err, errAlgoliaBadRequest) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// algoliaMultiQuery answers the batched multi-query endpoint. Every
// entry in the batch gets its own result, the way Algolia answers.
func algoliaMultiQuery(ctx context.Context, svc serviceAPI, body []byte) ([]algoliaResult, error) {
	var req algoliaMultiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("%w: invalid request body: %w", errAlgoliaBadRequest, err)
	}
	if len(req.Requests) == 0 {
		return nil, fmt.Errorf("%w: no requests in body", errAlgoliaBadRequest)
	}

	results := make([]algoliaResult, 0, len(req.Requests))
	for _, r := range req.Requests {
		values, err := url.ParseQuery(r.Params)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid params: %w", errAlgoliaBadRequest, err)
		}
		p := parseAlgoliaParams(values)
		// The Algolia client sends the query next to `params`
		// instead of inside it; `params` wins when both are set.
		if p.Query == "" {
			p.Query = r.Query
		}
		p.RawParams = r.Params

		name := r.IndexName
		if name == "" {
			name = algoliaDefaultIndexName
		}
		result, err := algoliaSearch(ctx, svc, name, p)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// algoliaSingleQuery answers the single-index search endpoint.
func algoliaSingleQuery(ctx context.Context, svc serviceAPI, indexName string, body []byte) (algoliaResult, error) {
	var req algoliaSingleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return algoliaResult{}, fmt.Errorf("%w: invalid request body: %w", errAlgoliaBadRequest, err)
	}

	p := algoliaParams{
		Query:                 req.Query,
		HitsPerPage:           algoliaDefaultHitsPerPage,
		AttributesToRetrieve:  req.AttributesToRetrieve,
		AttributesToHighlight: req.AttributesToHighlight,
		AttributesToSnippet:   req.AttributesToSnippet,
		HighlightPreTag:       req.HighlightPreTag,
		HighlightPostTag:      req.HighlightPostTag,
		SnippetEllipsisText:   req.SnippetEllipsisText,
	}
	if req.HitsPerPage != nil {
		p.HitsPerPage = *req.HitsPerPage
	}
	if req.Page != nil {
		p.Page = *req.Page
	}
	algoliaApplyParamDefaults(&p)

	return algoliaSearch(ctx, svc, indexName, p)
}

// parseAlgoliaParams normalizes the query-string form of the
// Algolia search parameters.
func parseAlgoliaParams(values url.Values) algoliaParams {
	p := algoliaParams{
		HitsPerPage: algoliaDefaultHitsPerPage,
		Query:       values.Get("query"),
	}
	p.AttributesToRetrieve = algoliaStringList(values.Get("attributesToRetrieve"))
	p.AttributesToHighlight = algoliaStringList(values.Get("attributesToHighlight"))
	p.AttributesToSnippet = algoliaStringList(values.Get("attributesToSnippet"))
	p.HighlightPreTag = values.Get("highlightPreTag")
	p.HighlightPostTag = values.Get("highlightPostTag")
	p.SnippetEllipsisText = values.Get("snippetEllipsisText")
	if v := values.Get("hitsPerPage"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.HitsPerPage = n
		}
	}
	if v := values.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.Page = n
		}
	}
	algoliaApplyParamDefaults(&p)
	return p
}

// algoliaApplyParamDefaults fills in the values Algolia defaults
// when the caller leaves them out.
func algoliaApplyParamDefaults(p *algoliaParams) {
	if p.HitsPerPage <= 0 {
		p.HitsPerPage = algoliaDefaultHitsPerPage
	}
	if p.HitsPerPage > algoliaMaxHits {
		// Algolia caps hitsPerPage at 1000; so do we, so the
		// reported page size matches the hits we can serve.
		p.HitsPerPage = algoliaMaxHits
	}
	if p.Page < 0 {
		p.Page = 0
	}
	if p.HighlightPreTag == "" {
		p.HighlightPreTag = "<em>"
	}
	if p.HighlightPostTag == "" {
		p.HighlightPostTag = "</em>"
	}
	if p.SnippetEllipsisText == "" {
		p.SnippetEllipsisText = "…"
	}
}

// algoliaSearch runs the ragabast hybrid search and shapes the
// outcome into the Algolia response envelope.
//
// nbHits is the number of hits the search returned, not the size of
// the corpus: ragabast ranks on demand and has no cheap total
// count, so exhaustiveNbHits is reported as false.
func algoliaSearch(ctx context.Context, svc serviceAPI, indexName string, p algoliaParams) (algoliaResult, error) {
	started := time.Now()

	result := algoliaResult{
		Hits:        []map[string]any{},
		Page:        p.Page,
		HitsPerPage: p.HitsPerPage,
		Query:       p.Query,
		Params:      p.RawParams,
		Index:       indexName,
	}

	// Algolia answers an empty query with every record in the
	// index. ragabast has no cheap "match everything" query, so
	// the compat layer answers with an empty page rather than
	// pretending to enumerate the corpus.
	if strings.TrimSpace(p.Query) == "" {
		result.ProcessingTimeMS = int(time.Since(started).Milliseconds())
		return result, nil
	}

	// Page N needs the first (N+1) hits, so ask the backend for
	// them and slice locally — the vector layer has no offset.
	limit := min((p.Page+1)*p.HitsPerPage, algoliaMaxHits)

	hits, err := svc.HybridSearch(ctx, p.Query, limit, service.SearchFilters{}, service.ModeHybrid)
	if err != nil {
		// A broken backend must not look like "no results":
		// the DocSearch UI shows an error screen for a failed
		// request and an empty-results screen for an empty
		// answer.
		return result, fmt.Errorf("search failed: %w", err)
	}

	result.NbHits = len(hits)
	result.NbPages = (result.NbHits + p.HitsPerPage - 1) / p.HitsPerPage

	terms := algoliaTerms(p.Query)
	start := p.Page * p.HitsPerPage
	if start < len(hits) {
		end := min(len(hits), start+p.HitsPerPage)
		for _, h := range hits[start:end] {
			result.Hits = append(result.Hits, algoliaHit(h, p, terms))
		}
	}

	result.ProcessingTimeMS = int(time.Since(started).Milliseconds())
	return result, nil
}

// algoliaHit converts one ragabast search result into an Algolia
// record with the DocSearch attribute layout, then decorates it
// with the highlight and snippet maps.
//
// The DocSearch client renders hit attributes as HTML (it falls
// back to the raw attribute when no snippet exists), and the chunk
// body comes from an ingested document the operator does not
// control. Every text attribute is therefore HTML-escaped on the
// way out — the highlight and snippet values are computed from the
// raw text and carry their own single escaping.
func algoliaHit(r models.SearchResult, p algoliaParams, terms []string) map[string]any {
	hitURL := algoliaHitURL(r)

	record := map[string]any{
		"objectID":           r.ChunkID,
		"content":            r.Content,
		"type":               "content",
		"url":                hitURL,
		"url_without_anchor": algoliaURLWithoutAnchor(hitURL),
		"anchor":             algoliaAnchor(hitURL),
		"hierarchy":          algoliaHierarchy(r),
	}

	visible := algoliaFilterRecord(record, p.AttributesToRetrieve)

	// The decorations are computed from the raw record so the
	// text is escaped exactly once.
	decorations := algoliaDecorations(record, visible, p, terms)

	algoliaEscapeText(visible)
	for path, value := range decorations {
		algoliaSetAttribute(visible, path, value)
	}

	return visible
}

// algoliaDecorations computes the _highlightResult and
// _snippetResult entries for a hit, keyed by the dotted path they
// are stored under.
//
// The highlight attributes default to every retrievable leaf —
// Algolia highlights all of them when the caller names none.
func algoliaDecorations(record, visible map[string]any, p algoliaParams, terms []string) map[string]any {
	decorations := map[string]any{}

	highlightAttrs := p.AttributesToHighlight
	if len(highlightAttrs) == 0 {
		highlightAttrs = algoliaLeafAttributes(visible)
	}
	for _, attr := range highlightAttrs {
		text, ok := algoliaTextAttribute(record, attr)
		if !ok {
			continue
		}
		highlighted, matched := algoliaHighlight(text, terms, p.HighlightPreTag, p.HighlightPostTag)
		decorations["_highlightResult."+attr] = map[string]any{
			"value":        highlighted,
			"matchLevel":   algoliaMatchLevel(matched),
			"matchedWords": []string{},
		}
	}

	for _, spec := range algoliaSnippetSpecs(p.AttributesToSnippet) {
		text, ok := algoliaTextAttribute(record, spec.attribute)
		if !ok {
			continue
		}
		decorations["_snippetResult."+spec.attribute] = map[string]any{
			"value": algoliaSnippet(text, terms, spec.size, p.HighlightPreTag, p.HighlightPostTag, p.SnippetEllipsisText),
		}
	}

	return decorations
}

// algoliaTextAttribute reads a string attribute out of a record.
func algoliaTextAttribute(record map[string]any, path string) (string, bool) {
	value, ok := algoliaPickAttribute(record, path)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// algoliaEscapeText HTML-escapes the text attributes of a record
// in place. The URL attributes are left alone: they are used as
// link targets, not as markup, and are restricted to http(s) by
// algoliaHitURL.
func algoliaEscapeText(record map[string]any) {
	if text, ok := record["content"].(string); ok {
		record["content"] = html.EscapeString(text)
	}
	hierarchy, ok := record["hierarchy"].(map[string]any)
	if !ok {
		return
	}
	for key, value := range hierarchy {
		if text, ok := value.(string); ok {
			hierarchy[key] = html.EscapeString(text)
		}
	}
}

// algoliaHierarchy maps a chunk onto the DocSearch hierarchy
// levels: lvl0 is the page title, lvl1..lvl6 the heading path
// below it.
func algoliaHierarchy(r models.SearchResult) map[string]any {
	hierarchy := map[string]any{"lvl0": r.DocumentTitle}
	for i := 1; i <= 6; i++ {
		hierarchy[fmt.Sprintf("lvl%d", i)] = ""
	}
	for i, segment := range algoliaHeaderSegments(r) {
		if i >= 6 {
			break
		}
		hierarchy[fmt.Sprintf("lvl%d", i+1)] = segment
	}
	return hierarchy
}

// algoliaHeaderSegments splits the chunk's header path into the
// heading segments below the page title.
//
// The chunker builds the path from the document's headings and
// starts with the page's own H1, which is already the document
// title — that leading segment is dropped so lvl1 is the first
// section heading rather than a repeat of lvl0.
func algoliaHeaderSegments(r models.SearchResult) []string {
	if strings.TrimSpace(r.HeaderPath) == "" {
		return nil
	}
	parts := strings.Split(r.HeaderPath, " > ")
	if len(parts) > 0 && strings.TrimSpace(parts[0]) == strings.TrimSpace(r.DocumentTitle) {
		parts = parts[1:]
	}
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			segments = append(segments, p)
		}
	}
	return segments
}

// algoliaHitURL returns the citation URL for a search result,
// restricted to http(s). The DocSearch client renders the hit as an
// <a href>, so a javascript: or data: URL coming from an ingested
// document would be an XSS vector on the docs site.
func algoliaHitURL(r models.SearchResult) string {
	candidates := make([]string, 0, 2+len(r.DocumentURLs))
	candidates = append(candidates, r.CitationURL, r.DocbuilderURL)
	candidates = append(candidates, r.DocumentURLs...)
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		u, err := url.Parse(c)
		if err != nil {
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		return c
	}
	return ""
}

// algoliaURLWithoutAnchor strips the fragment from a URL.
func algoliaURLWithoutAnchor(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// algoliaAnchor returns the fragment of a URL.
func algoliaAnchor(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Fragment
}

// algoliaFilterRecord restricts a record to the requested
// attributes. Algolia returns only what the caller asked for, plus
// objectID, which is always present.
func algoliaFilterRecord(record map[string]any, attrs []string) map[string]any {
	if len(attrs) == 0 {
		return record
	}
	if slices.Contains(attrs, "*") {
		return record
	}

	filtered := map[string]any{"objectID": record["objectID"]}
	for _, a := range attrs {
		value, ok := algoliaPickAttribute(record, a)
		if !ok {
			continue
		}
		algoliaSetAttribute(filtered, a, value)
	}
	return filtered
}

// algoliaPickAttribute reads a dotted attribute path out of a
// record.
func algoliaPickAttribute(record map[string]any, path string) (any, bool) {
	var current any = record
	for part := range strings.SplitSeq(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// algoliaSetAttribute writes a value into a record at a dotted
// attribute path, creating the intermediate maps.
func algoliaSetAttribute(record map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := record
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

// algoliaLeafAttributes lists the dotted paths of every string leaf
// in a record. It is the default highlight set: Algolia highlights
// every retrievable attribute when the caller does not name any.
func algoliaLeafAttributes(record map[string]any) []string {
	var out []string
	for key, value := range record {
		if key == "objectID" || strings.HasPrefix(key, "_") {
			continue
		}
		switch v := value.(type) {
		case string:
			out = append(out, key)
		case map[string]any:
			for _, leaf := range algoliaLeafAttributes(v) {
				out = append(out, key+"."+leaf)
			}
		}
	}
	return out
}

// algoliaStringList parses an Algolia list parameter. The Algolia
// client serializes arrays as a JSON array string; the REST docs
// also allow a comma-separated list. Both forms are accepted.
func algoliaStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			return list
		}
	}
	parts := strings.Split(raw, ",")
	list := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			list = append(list, p)
		}
	}
	return list
}

// algoliaSnippetSpec is one attributesToSnippet entry: the
// attribute to snippet plus the window size in words.
type algoliaSnippetSpec struct {
	attribute string
	size      int
}

// algoliaSnippetSpecs parses the attributesToSnippet parameter.
func algoliaSnippetSpecs(raw []string) []algoliaSnippetSpec {
	specs := make([]algoliaSnippetSpec, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		spec := algoliaSnippetSpec{attribute: entry, size: algoliaDefaultSnippetSize}
		if attr, size, ok := strings.Cut(entry, ":"); ok {
			spec.attribute = attr
			if n, err := strconv.Atoi(size); err == nil && n > 0 {
				spec.size = n
			}
		}
		specs = append(specs, spec)
	}
	return specs
}

// algoliaTerms splits a query into lowercase search terms.
func algoliaTerms(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		terms = append(terms, strings.ToLower(f))
	}
	return terms
}

// algoliaHighlight wraps every query-term match in the text with
// the pre/post tags and reports whether anything matched.
//
// The text is HTML-escaped: the DocSearch client renders the value
// with dangerouslySetInnerHTML, so an unescaped chunk body would be
// an XSS vector on the docs site.
func algoliaHighlight(text string, terms []string, pre, post string) (string, bool) {
	ranges := algoliaMatchRanges(text, terms)

	var b strings.Builder
	prev := 0
	for _, r := range ranges {
		b.WriteString(html.EscapeString(text[prev:r[0]]))
		b.WriteString(pre)
		b.WriteString(html.EscapeString(text[r[0]:r[1]]))
		b.WriteString(post)
		prev = r[1]
	}
	b.WriteString(html.EscapeString(text[prev:]))

	return b.String(), len(ranges) > 0
}

// algoliaMatchLevel maps a match outcome onto Algolia's matchLevel
// vocabulary.
func algoliaMatchLevel(matched bool) string {
	if matched {
		return "full"
	}
	return "none"
}

// algoliaSnippet returns a window of at most size words around the
// first query-term match, with the matched words wrapped in the
// highlight tags and the truncation marked by ellipses.
func algoliaSnippet(text string, terms []string, size int, pre, post, ellipsis string) string {
	words := algoliaWords(text)
	if len(words) == 0 || size <= 0 {
		return ""
	}

	start := 0
	for i, w := range words {
		if algoliaWordMatches(text[w[0]:w[1]], terms) {
			start = max(0, i-size/2)
			break
		}
	}
	end := min(len(words), start+size)

	snippet, _ := algoliaHighlight(text[words[start][0]:words[end-1][1]], terms, pre, post)
	if start > 0 {
		snippet = ellipsis + snippet
	}
	if end < len(words) {
		snippet = snippet + ellipsis
	}
	return snippet
}

// algoliaMatchRanges returns the byte ranges of the words that
// match a query term. A word matches when it starts with the term,
// case-insensitively — Algolia matches whole words and prefixes.
func algoliaMatchRanges(text string, terms []string) [][2]int {
	var out [][2]int
	for _, w := range algoliaWords(text) {
		if algoliaWordMatches(text[w[0]:w[1]], terms) {
			out = append(out, w)
		}
	}
	return out
}

// algoliaWordMatches reports whether a word matches any term.
func algoliaWordMatches(word string, terms []string) bool {
	lower := strings.ToLower(word)
	for _, t := range terms {
		if t != "" && strings.HasPrefix(lower, t) {
			return true
		}
	}
	return false
}

// algoliaWords splits the text into word byte ranges. Bytes above
// ASCII are treated as word characters so a UTF-8 word stays one
// word.
func algoliaWords(text string) [][2]int {
	var out [][2]int
	for i := 0; i < len(text); {
		if !algoliaWordByte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && algoliaWordByte(text[j]) {
			j++
		}
		out = append(out, [2]int{i, j})
		i = j
	}
	return out
}

// algoliaWordByte reports whether a byte belongs to a word.
func algoliaWordByte(c byte) bool {
	return c == '_' || c >= 0x80 ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// algoliaWriteError answers with Algolia's error shape: a JSON
// object carrying a "message" field.
func algoliaWriteError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
