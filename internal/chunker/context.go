package chunker

import (
	"fmt"
	"strings"

	"github.com/ragabast/internal/models"
)

// ContextPreserver manages hierarchical context across chunks.
type ContextPreserver struct {
	maxContextDepth int
}

// NewContextPreserver creates a new context preserver.
func NewContextPreserver(maxDepth int) *ContextPreserver {
	if maxDepth <= 0 {
		maxDepth = 3 // Default to 3 levels
	}
	return &ContextPreserver{
		maxContextDepth: maxDepth,
	}
}

// PreserveContext adds parent context to child chunks.
func (cp *ContextPreserver) PreserveContext(chunks []*models.Chunk) []*models.Chunk {
	if len(chunks) == 0 {
		return chunks
	}

	// Build chunk map for quick lookup
	chunkMap := make(map[string]*models.Chunk)
	for _, chunk := range chunks {
		chunkMap[chunk.ID] = chunk
	}

	// Process each chunk to add context
	for _, chunk := range chunks {
		if chunk.ParentID != "" {
			context := cp.buildContextChain(chunk, chunkMap, 0)
			if context != "" {
				// Prepend context to content with a separator
				chunk.Content = context + "\n\n---\n\n" + chunk.Content
			}
		}
	}

	return chunks
}

// buildContextChain recursively builds the context chain from parent chunks.
func (cp *ContextPreserver) buildContextChain(chunk *models.Chunk, chunkMap map[string]*models.Chunk, depth int) string {
	if depth >= cp.maxContextDepth || chunk.ParentID == "" {
		return ""
	}

	parent, exists := chunkMap[chunk.ParentID]
	if !exists {
		return ""
	}

	// Build context from parent
	var contextParts []string

	// Add parent header/path
	if parent.HeaderPath != "" {
		contextParts = append(contextParts, fmt.Sprintf("[%s]", parent.HeaderPath))
	}

	// Add a snippet of parent content (first 200 chars)
	contentSnippet := parent.Content
	if len(contentSnippet) > 200 {
		contentSnippet = contentSnippet[:200] + "..."
	}
	contextParts = append(contextParts, contentSnippet)

	// Recursively get grandparent context
	grandparentContext := cp.buildContextChain(parent, chunkMap, depth+1)
	if grandparentContext != "" {
		contextParts = append([]string{grandparentContext}, contextParts...)
	}

	return strings.Join(contextParts, "\n\n")
}

// ExtractHierarchicalInfo extracts hierarchical information from chunks.
func (cp *ContextPreserver) ExtractHierarchicalInfo(chunks []*models.Chunk) *models.HierarchicalInfo {
	info := &models.HierarchicalInfo{
		RootChunks:  []string{},
		ChildMap:    make(map[string][]string),
		LevelMap:    make(map[int][]string),
		HeaderPaths: make(map[string]string),
	}

	// Build relationships
	for _, chunk := range chunks {
		if chunk.ParentID == "" {
			info.RootChunks = append(info.RootChunks, chunk.ID)
		} else {
			info.ChildMap[chunk.ParentID] = append(info.ChildMap[chunk.ParentID], chunk.ID)
		}

		info.LevelMap[chunk.Level] = append(info.LevelMap[chunk.Level], chunk.ID)
		info.HeaderPaths[chunk.ID] = chunk.HeaderPath
	}

	return info
}

// GetContextForChunk returns the full context for a specific chunk.
func (cp *ContextPreserver) GetContextForChunk(targetChunk *models.Chunk, allChunks []*models.Chunk) string {
	chunkMap := make(map[string]*models.Chunk)
	for _, chunk := range allChunks {
		chunkMap[chunk.ID] = chunk
	}

	var contextParts []string

	// Add parent context
	if targetChunk.ParentID != "" {
		parentContext := cp.buildContextChain(targetChunk, chunkMap, 0)
		if parentContext != "" {
			contextParts = append(contextParts, parentContext)
		}
	}

	// Add current chunk's header
	if targetChunk.HeaderPath != "" {
		contextParts = append(contextParts, fmt.Sprintf("Current: [%s]", targetChunk.HeaderPath))
	}

	// Add sibling context (brief overview of other chunks at same level)
	siblings := cp.findSiblings(targetChunk, allChunks)
	if len(siblings) > 0 {
		siblingSummary := cp.summarizeSiblings(siblings)
		if siblingSummary != "" {
			contextParts = append(contextParts, "Related: "+siblingSummary)
		}
	}

	return strings.Join(contextParts, "\n\n")
}

// findSiblings finds chunks at the same hierarchical level as the target.
func (cp *ContextPreserver) findSiblings(target *models.Chunk, allChunks []*models.Chunk) []*models.Chunk {
	var siblings []*models.Chunk

	for _, chunk := range allChunks {
		if chunk.ID == target.ID {
			continue
		}

		// Same parent means direct siblings
		if chunk.ParentID == target.ParentID && chunk.ParentID != "" {
			siblings = append(siblings, chunk)
			continue
		}

		// Same level and same parent level
		if chunk.Level == target.Level && chunk.ParentID == target.ParentID {
			siblings = append(siblings, chunk)
		}
	}

	return siblings
}

// summarizeSiblings creates a brief summary of sibling chunks.
func (cp *ContextPreserver) summarizeSiblings(siblings []*models.Chunk) string {
	if len(siblings) == 0 {
		return ""
	}

	var summaries []string
	for _, sibling := range siblings {
		if sibling.HeaderPath != "" {
			summaries = append(summaries, sibling.HeaderPath)
		}
	}

	if len(summaries) == 0 {
		return ""
	}

	return strings.Join(summaries, ", ")
}

// BuildDocumentTree creates a tree structure from chunks.
func (cp *ContextPreserver) BuildDocumentTree(chunks []*models.Chunk) *models.DocumentTree {
	tree := &models.DocumentTree{
		Root:  nil,
		Nodes: make(map[string]*models.TreeNode),
		Level: 0,
	}

	// Create nodes
	for _, chunk := range chunks {
		node := &models.TreeNode{
			ChunkID:  chunk.ID,
			Content:  chunk.Content[:min(100, len(chunk.Content))] + "...",
			Level:    chunk.Level,
			Children: []*models.TreeNode{},
		}
		tree.Nodes[chunk.ID] = node
	}

	// Build relationships
	for _, chunk := range chunks {
		node := tree.Nodes[chunk.ID]
		if chunk.ParentID == "" {
			// Root node
			if tree.Root == nil {
				tree.Root = node
			}
		} else {
			// Add to parent's children
			if parent, exists := tree.Nodes[chunk.ParentID]; exists {
				parent.Children = append(parent.Children, node)
			}
		}
	}

	return tree
}

// GetBreadcrumbPath returns the breadcrumb path for a chunk.
func (cp *ContextPreserver) GetBreadcrumbPath(chunk *models.Chunk, allChunks []*models.Chunk) string {
	chunkMap := make(map[string]*models.Chunk)
	for _, c := range allChunks {
		chunkMap[c.ID] = c
	}

	var path []string
	current := chunk

	for current != nil {
		if current.HeaderPath != "" {
			path = append([]string{current.HeaderPath}, path...)
		}

		if current.ParentID == "" {
			break
		}

		parent, exists := chunkMap[current.ParentID]
		if !exists {
			break
		}
		current = parent
	}

	return strings.Join(path, " > ")
}
