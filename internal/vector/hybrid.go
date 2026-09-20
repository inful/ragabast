package vector

import (
	"context"
	"fmt"
	"sort"

	"github.com/ragabast/internal/models"
)

// SearchMode selects how the search ranking is produced. The
// default for new callers is ModeHybrid; the existing
// Service.Search method pins to ModeSemantic for backward
// compatibility with the v0.3.0 API.
type SearchMode int

const (
	// ModeHybrid runs both keyword and semantic rankings and
	// fuses them via Reciprocal Rank Fusion (RRF) with k=60.
	// This is the recommended mode for technical Markdown
	// corpora where exact-term recall and semantic recall
	// each cover gaps the other misses.
	ModeHybrid SearchMode = iota
	// ModeSemantic runs only the embedding-based vector DB
	// search. Equivalent to the v0.3.0 search behavior;
	// kept as a mode knob for callers that want pure
	// semantic ranking.
	ModeSemantic
	// ModeKeyword runs only the bleve keyword search. Useful
	// when a query is identifier-shaped and semantic
	// ranking adds noise.
	ModeKeyword
)

// String returns the lower-case canonical name for each mode,
// matching the JSON wire values the HTTP API exposes.
func (m SearchMode) String() string {
	switch m {
	case ModeHybrid:
		return "hybrid"
	case ModeSemantic:
		return "semantic"
	case ModeKeyword:
		return "keyword"
	default:
		return "unknown"
	}
}

// rrfK is the standard RRF constant from Cormack et al.
// (2009). Values in [40, 100] are typical; we use 60 because
// it dampens high-rank contributions enough to keep a chunk
// that ranks #1 in one list and #50 in the other from
// drowning out a chunk that ranks #5 in both.
const rrfK = 60

// rrfItem is one entry in a fused ranking.
type rrfItem struct {
	ID    string
	Score float64
}

// rrfFuse merges two ranked ID lists into a single fused
// ranking using Reciprocal Rank Fusion:
//
//	score(d) = Σ  1 / (k + rank_in_R(d))
//
// for every ranking R that contains d. Documents that appear
// in BOTH rankings naturally rank higher than documents that
// appear in only one. The function is deterministic — same
// inputs always yield the same fused ranking.
//
// We index documents by their rank in each list (1-based), so
// a doc at position 0 in semantic and position 2 in keyword
// receives score 1/(k+1) + 1/(k+3). The result is sorted
// descending by score.
func rrfFuse(semantic, keyword []string, k int) []rrfItem {
	scores := make(map[string]float64)
	add := func(rank int, id string) {
		if id == "" {
			return
		}
		scores[id] += 1.0 / float64(k+rank)
	}
	for i, id := range semantic {
		add(i+1, id)
	}
	for i, id := range keyword {
		add(i+1, id)
	}
	out := make([]rrfItem, 0, len(scores))
	for id, sc := range scores {
		out = append(out, rrfItem{ID: id, Score: sc})
	}
	// Stable sort with an ID tiebreaker so two chunks with
	// the same RRF score always come out in the same order.
	// Without the tiebreaker, sort.Slice is non-stable and
	// map iteration is randomized — the same query on the
	// same data would occasionally flip two tied hits.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// searchHybridResult mirrors models.SearchResult so the
// hybrid tests don't depend on the models package for
// type identity. The production code builds
// models.SearchResult values directly; the test helper
// exists to avoid importing models from a test that
// primarily exercises RRF + enrichment.
//
// Defined as a type alias so existing assertions can use
// the same struct fields without conversion.
type searchHybridResult = models.SearchResult

// SearchHybrid runs the configured search mode. The keyword
// side is consulted when mode is ModeHybrid or ModeKeyword,
// and silently skipped when the SearchIndex is nil (graceful
// degradation to pure semantic search).
//
// For mode=hybrid, both rankings are fetched (each up to
// limit × 2 candidates to give RRF enough headroom), fused
// via rrfFuse, then the top `limit` are enriched with full
// chunk metadata via a per-hit lookup against the vector DB.
//
// Filters are pre-applied to BOTH rankings; chunks failing
// the filter never appear in the fused result.
func (vo *VectorOperations) SearchHybrid(
	ctx context.Context,
	query string,
	limit int,
	filters SearchFilters,
	mode SearchMode,
) ([]models.SearchResult, error) {
	if query == "" {
		return nil, models.ErrSearchFailed
	}
	if limit <= 0 {
		limit = 5
	}

	switch mode {
	case ModeKeyword:
		return vo.searchKeywordOnly(ctx, query, limit, filters)
	case ModeSemantic:
		return vo.searchSemanticOnly(ctx, query, limit, filters)
	case ModeHybrid:
		return vo.searchHybridRRF(ctx, query, limit, filters)
	default:
		return nil, fmt.Errorf("unknown search mode: %d", mode)
	}
}

// searchSemanticOnly is the v0.3.0 path: pure vector DB search.
// Filters are translated to the chromem-go Where map.
func (vo *VectorOperations) searchSemanticOnly(
	ctx context.Context,
	query string,
	limit int,
	filters SearchFilters,
) ([]models.SearchResult, error) {
	embedding, err := vo.embeddings.GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}
	results, err := vo.db.Search(ctx, embedding, limit, filters.toWhere())
	if err != nil {
		return nil, fmt.Errorf("semantic search failed: %w", err)
	}
	return results, nil
}

// searchKeywordOnly runs only the bleve keyword search and
// enriches each hit with metadata from the vector DB. Chunks
// that exist only in the keyword index (e.g. failed
// embeddings) appear here; chunks that exist only in the
// vector DB do not.
//
// When the SearchIndex is nil we fall back to semantic search
// with a warning. Operationally: hybrid isn't optional in
// production, but tests can pass through this method with no
// keyword side wired.
func (vo *VectorOperations) searchKeywordOnly(
	ctx context.Context,
	query string,
	limit int,
	filters SearchFilters,
) ([]models.SearchResult, error) {
	if vo.searchIndex == nil {
		return vo.searchSemanticOnly(ctx, query, limit, filters)
	}
	hits, err := vo.searchIndex.Search(query, limit, filters)
	if err != nil {
		return nil, fmt.Errorf("keyword search failed: %w", err)
	}
	return vo.enrichKeywordHits(ctx, hits)
}

// searchHybridRRF runs both rankings, fuses via RRF, and
// enriches the top results. The two sides each fetch
// limit*2 candidates so the fused top-K has enough headroom
// when one ranking is sparse.
func (vo *VectorOperations) searchHybridRRF(
	ctx context.Context,
	query string,
	limit int,
	filters SearchFilters,
) ([]models.SearchResult, error) {
	const headroom = 2 // each side fetches limit*headroom candidates

	// Keyword side first: it's cheaper and gracefully skips
	// if the SearchIndex isn't wired.
	var keywordIDs []string
	if vo.searchIndex != nil {
		hits, err := vo.searchIndex.Search(query, limit*headroom, filters)
		if err != nil {
			return nil, fmt.Errorf("keyword search failed: %w", err)
		}
		keywordIDs = make([]string, 0, len(hits))
		for _, h := range hits {
			keywordIDs = append(keywordIDs, h.ID)
		}
	}

	// Semantic side: requires the embeddings call. If it
	// fails, degrade to keyword-only — better than 500ing
	// on a transient embeddings-server hiccup.
	semanticIDs := []string{}
	if vo.embeddings != nil {
		embedding, err := vo.embeddings.GenerateEmbedding(ctx, query)
		if err == nil {
			results, err := vo.db.Search(ctx, embedding, limit*headroom, filters.toWhere())
			if err == nil {
				semanticIDs = make([]string, 0, len(results))
				for _, r := range results {
					semanticIDs = append(semanticIDs, r.ChunkID)
				}
			}
		}
	}

	// Nothing on either side → empty result.
	if len(semanticIDs) == 0 && len(keywordIDs) == 0 {
		return []models.SearchResult{}, nil
	}

	fused := rrfFuse(semanticIDs, keywordIDs, rrfK)
	if limit > 0 && len(fused) > limit {
		fused = fused[:limit]
	}

	// Enrich with full metadata from the vector DB. Hits
	// that exist only in the keyword index fall back to a
	// minimal SearchResult (no content/title/URLs), so the
	// caller can distinguish "found in keyword side, missing
	// from vector DB" from "found in both".
	return vo.enrichFusedResults(ctx, fused)
}

// enrichKeywordHits turns bleve hits into SearchResults by
// looking up each chunk in the vector DB. Missing chunks
// become minimal SearchResults so callers can still see the
// match (this can happen when the embeddings step of an
// ingest failed but the chunk was still indexed for keyword
// search — a partial-state signal).
func (vo *VectorOperations) enrichKeywordHits(
	ctx context.Context,
	hits []SearchHit,
) ([]models.SearchResult, error) {
	out := make([]models.SearchResult, 0, len(hits))
	for _, h := range hits {
		chunk, err := vo.db.GetChunk(ctx, h.ID)
		if err != nil || chunk == nil {
			// Fall back to a minimal result so the match
			// still surfaces to the caller.
			out = append(out, models.SearchResult{
				ChunkID:    h.ID,
				DocumentID: h.DocumentID,
			})
			continue
		}
		out = append(out, chunkToSearchResult(chunk, h.Score))
	}
	return out, nil
}

// enrichFusedResults turns a fused ranking back into
// SearchResults. Same enrichment strategy as
// enrichKeywordHits — hits missing from the vector DB become
// minimal SearchResults so the match is still visible.
func (vo *VectorOperations) enrichFusedResults(
	ctx context.Context,
	fused []rrfItem,
) ([]models.SearchResult, error) {
	out := make([]models.SearchResult, 0, len(fused))
	for _, it := range fused {
		chunk, err := vo.db.GetChunk(ctx, it.ID)
		if err != nil || chunk == nil {
			out = append(out, models.SearchResult{
				ChunkID:    it.ID,
				DocumentID: "",
			})
			continue
		}
		out = append(out, chunkToSearchResult(chunk, float32(it.Score)))
	}
	return out, nil
}

// chunkToSearchResult converts a stored chunk into the
// search-result shape callers expect. The score is whatever
// the caller passed in (BM25 score for keyword side, RRF
// fused score for hybrid).
func chunkToSearchResult(chunk *models.Chunk, score float32) models.SearchResult {
	return models.SearchResult{
		ChunkID:            chunk.ID,
		DocumentID:         chunk.DocumentID,
		Content:            chunk.Content,
		HeaderPath:         chunk.HeaderPath,
		Level:              chunk.Level,
		StartLine:          chunk.StartLine,
		EndLine:            chunk.EndLine,
		DocumentTitle:      chunk.DocumentTitle,
		DocumentURLs:       chunk.DocumentURLs,
		DocumentTags:       chunk.DocumentTags,
		DocumentCategories: chunk.DocumentCategories,
		ParentID:           chunk.ParentID,
		Similarity:         score,
		Fingerprint:        chunk.Fingerprint,
		UID:                chunk.UID,
	}
}

// toWhere translates SearchFilters into the chromem-go
// Where map. Mirrors the helper in internal/service/query.go
// but kept local to avoid an import cycle (vector is a lower
// package than service).
func (f SearchFilters) toWhere() map[string]string {
	out := map[string]string{}
	if d := trim(f.DocumentID); d != "" {
		out["document_id"] = d
	}
	if t := trim(f.Tag); t != "" {
		out["document_tags"] = t
	}
	if c := trim(f.Category); c != "" {
		out["document_categories"] = c
	}
	return out
}

func trim(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
