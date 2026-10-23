package chunker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
)

// Chunker implements H1/H2-based document chunking with hierarchical context preservation.
type Chunker struct {
	parser  *parser.DocbuilderParser
	maxSize int
	minSize int
	overlap int
}

// NewChunker creates a new chunker with the specified configuration.
func NewChunker(maxSize, minSize, overlap int) *Chunker {
	return &Chunker{
		parser:  parser.NewDocbuilderParser(),
		maxSize: maxSize,
		minSize: minSize,
		overlap: overlap,
	}
}

// generateChunkID creates a deterministic ID for a chunk based on its content and metadata.
//
// It is intentionally stable across runs to make repeated ingests dedupe-friendly.
func generateChunkID(documentID, headerPath string, startLine, endLine int, content string) string {
	data := documentID + "|" + headerPath + "|" + strconv.Itoa(startLine) + "|" + strconv.Itoa(endLine) + "|" + content
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes for shorter IDs.
}

// ChunkDocument splits a document into chunks based on H1 and H2 headers.
func (c *Chunker) ChunkDocument(doc *models.Document) ([]*models.Chunk, error) {
	if doc == nil || doc.Content == "" {
		return nil, errors.New("document content is empty")
	}

	// Extract headers to understand document structure
	headers := c.parser.ExtractHeaders(doc.Content)

	// If no headers, treat as single chunk
	if len(headers) == 0 {
		return c.createSingleChunk(doc)
	}

	// Split document by headers
	return c.splitByHeaders(doc, headers)
}

// createSingleChunk creates a single chunk for documents without headers.
func (c *Chunker) createSingleChunk(doc *models.Document) ([]*models.Chunk, error) {
	content := doc.Content

	// If content is too large, split into smaller chunks
	if len(content) > c.maxSize {
		return c.splitBySize(doc, content)
	}

	chunk := models.NewChunk()
	chunk.ID = generateChunkID(doc.ID, doc.Title, 0, strings.Count(content, "\n"), content)
	chunk.DocumentID = doc.ID
	chunk.Content = content
	chunk.HeaderPath = doc.Title
	chunk.Level = 0
	chunk.StartLine = 0
	chunk.EndLine = strings.Count(content, "\n")

	return []*models.Chunk{chunk}, nil
}

// splitByHeaders splits the document based on header positions.
func (c *Chunker) splitByHeaders(doc *models.Document, headers []models.HeaderInfo) ([]*models.Chunk, error) {
	var chunks []*models.Chunk
	content := doc.Content
	lines := strings.Split(content, "\n")

	// Create a map of header positions
	headerMap := make(map[int]models.HeaderInfo)
	for _, h := range headers {
		headerMap[h.Line] = h
	}

	// Process sections
	currentChunk := models.NewChunk()
	currentChunk.DocumentID = doc.ID
	currentChunk.Level = 0
	currentChunk.HeaderPath = doc.Title
	sectionLines := []string{}
	currentLine := 0

	for i, line := range lines {
		// Check if this line is a header
		if header, isHeader := headerMap[i]; isHeader {
			// If we have content in current chunk, save it.
			//
			// Every section is emitted, whatever its size:
			// min_chunk_size is a merge threshold (handled by
			// mergeSmallChunks below), never a drop threshold.
			// Dropping short sections here silently removed
			// them from the index — a document whose sections
			// were all short produced zero chunks and the
			// ingest still reported success.
			if len(sectionLines) > 0 {
				contentStr := strings.Join(sectionLines, "\n")
				chunk := c.createChunkFromSection(currentChunk, contentStr, currentLine, i-1)
				chunks = append(chunks, chunk)
			}

			// Start new chunk for this header
			currentChunk = models.NewChunk()
			currentChunk.DocumentID = doc.ID
			currentChunk.Level = header.Level
			currentChunk.HeaderPath = c.buildHeaderPath(headers, header)
			sectionLines = []string{line}
			currentLine = i
		} else {
			sectionLines = append(sectionLines, line)
		}
	}

	// Handle final section
	if len(sectionLines) > 0 {
		contentStr := strings.Join(sectionLines, "\n")
		chunk := c.createChunkFromSection(currentChunk, contentStr, currentLine, len(lines)-1)
		chunks = append(chunks, chunk)
	}

	// If chunks are too small, merge them
	chunks = c.mergeSmallChunks(chunks)

	return chunks, nil
}

// splitBySize splits content by size when no headers are present.
func (c *Chunker) splitBySize(doc *models.Document, content string) ([]*models.Chunk, error) {
	var chunks []*models.Chunk
	lines := strings.Split(content, "\n")

	var currentChunk []string
	currentSize := 0
	startLine := 0

	for i, line := range lines {
		lineSize := len(line) + 1 // +1 for newline

		// If adding this line would exceed max size and we have minimum content
		if currentSize+lineSize > c.maxSize && len(currentChunk) >= c.minSize/50 {
			// Create chunk
			chunk := models.NewChunk()
			chunkContent := strings.Join(currentChunk, "\n")
			chunk.ID = generateChunkID(doc.ID, doc.Title, startLine, i-1, chunkContent)
			chunk.DocumentID = doc.ID
			chunk.Content = chunkContent
			chunk.HeaderPath = doc.Title
			chunk.Level = 0
			chunk.StartLine = startLine
			chunk.EndLine = i - 1
			chunks = append(chunks, chunk)

			// Start new chunk with overlap
			if c.overlap > 0 && len(currentChunk) > 0 {
				overlapLines := c.calculateOverlapLines(currentChunk)
				currentChunk = append(overlapLines, line)
				currentSize = len(strings.Join(currentChunk, "\n"))
				startLine = i - len(overlapLines)
			} else {
				currentChunk = []string{line}
				currentSize = lineSize
				startLine = i
			}
		} else {
			currentChunk = append(currentChunk, line)
			currentSize += lineSize
		}
	}

	// Add final chunk
	if len(currentChunk) > 0 {
		chunk := models.NewChunk()
		chunkContent := strings.Join(currentChunk, "\n")
		chunk.ID = generateChunkID(doc.ID, doc.Title, startLine, len(lines)-1, chunkContent)
		chunk.DocumentID = doc.ID
		chunk.Content = chunkContent
		chunk.HeaderPath = doc.Title
		chunk.Level = 0
		chunk.StartLine = startLine
		chunk.EndLine = len(lines) - 1
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// createChunkFromSection creates a chunk from a section of content.
func (c *Chunker) createChunkFromSection(base *models.Chunk, content string, startLine, endLine int) *models.Chunk {
	chunk := models.NewChunk()
	chunk.ID = generateChunkID(base.DocumentID, base.HeaderPath, startLine, endLine, content)
	chunk.DocumentID = base.DocumentID
	chunk.Content = content
	chunk.HeaderPath = base.HeaderPath
	chunk.Level = base.Level
	chunk.StartLine = startLine
	chunk.EndLine = endLine
	chunk.ParentID = base.ParentID
	return chunk
}

// buildHeaderPath constructs the full hierarchical path for a header.
func (c *Chunker) buildHeaderPath(allHeaders []models.HeaderInfo, current models.HeaderInfo) string {
	var path []string

	// Find parent headers
	for _, h := range allHeaders {
		if h.Line < current.Line && h.Level < current.Level {
			// This is a parent header - add it to path
			path = append(path, h.Text)
		}
	}

	// Add current header
	path = append(path, current.Text)

	return strings.Join(path, " > ")
}

// mergeSmallChunks folds chunks that are below the minimum size
// into the chunk that precedes them.
//
// A chunk shorter than min_chunk_size cannot stand on its own, but
// its text must never leave the index: the chunk is appended to the
// previous chunk's content and the previous chunk's ID is
// regenerated to match the new content. The folded chunk's ID is
// kept in ChildrenIDs so the hierarchy still records it.
//
// The first chunk is always kept as-is: there is no previous chunk
// to fold it into, and dropping it would drop the whole document.
func (c *Chunker) mergeSmallChunks(chunks []*models.Chunk) []*models.Chunk {
	if len(chunks) == 0 {
		return chunks
	}

	merged := make([]*models.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if len(chunk.Content) >= c.minSize || len(merged) == 0 {
			merged = append(merged, chunk)
			continue
		}
		prev := merged[len(merged)-1]
		prev.Content += "\n" + chunk.Content
		prev.EndLine = chunk.EndLine
		prev.ChildrenIDs = append(prev.ChildrenIDs, chunk.ID)
		// Regenerate ID for the merged chunk.
		prev.ID = generateChunkID(prev.DocumentID, prev.HeaderPath, prev.StartLine, prev.EndLine, prev.Content)
	}

	return merged
}

// calculateOverlapLines returns the last N lines for overlap.
func (c *Chunker) calculateOverlapLines(chunk []string) []string {
	if c.overlap == 0 || len(chunk) == 0 {
		return []string{}
	}

	// Calculate how many lines represent the overlap size
	overlapChars := 0
	overlapLines := []string{}

	for i := len(chunk) - 1; i >= 0 && overlapChars < c.overlap; i-- {
		line := chunk[i]
		overlapChars += len(line) + 1
		overlapLines = append([]string{line}, overlapLines...)
	}

	return overlapLines
}

// ChunkWithHierarchy creates chunks and establishes parent-child relationships.
func (c *Chunker) ChunkWithHierarchy(doc *models.Document) ([]*models.Chunk, error) {
	chunks, err := c.ChunkDocument(doc)
	if err != nil {
		return nil, err
	}

	// Build hierarchy
	chunkMap := make(map[string]*models.Chunk)
	for _, chunk := range chunks {
		chunkMap[chunk.ID] = chunk
	}

	// Establish parent-child relationships
	for _, chunk := range chunks {
		if chunk.Level > 0 {
			// Find parent chunk (closest chunk with lower level before this one)
			var parent *models.Chunk
			for _, other := range chunks {
				if other.Level < chunk.Level && other.StartLine < chunk.StartLine {
					if parent == nil || other.StartLine > parent.StartLine {
						parent = other
					}
				}
			}
			if parent != nil {
				chunk.ParentID = parent.ID
				parent.AddChild(chunk.ID)
			}
		}
	}

	return chunks, nil
}
