---
uid: "sample-doc-001"
tags:
  - "example"
  - "tutorial"
  - "test"
categories:
  - "documentation"
  - "guides"
urls:
  - "https://example.com/docs/sample"
  - "https://github.com/ragabast"
created_at: "2026-01-12T22:00:00Z"
updated_at: "2026-01-12T22:00:00Z"
fingerprint: b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9
---

# Getting Started with Ragabast

Ragabast is a production-ready RAG/LLM information retrieval system built in Go.

## Introduction

This system provides:
- Document ingestion with custom "docubilder" format
- H1/H2-based chunking with hierarchical context
- Vector embeddings via Ollama (nomic-embed-text-v1.5)
- CLI and web interfaces
- Local LLM integration (gemma:2b)

## Installation

To install Ragabast, you need:
- Go 1.25 or later
- Ollama running locally
- chromem-go for vector storage

### Prerequisites

1. Install Go from official website
2. Install Ollama and pull required models
3. Clone the repository

## Usage

### CLI Commands

The system supports several CLI commands:

#### Ingest Documents

```bash
./ragabast ingest path/to/document.md
./ragabast ingest --path ./data/documents
```

#### Query with Natural Language

```bash
./ragabast query "What is Ragabast?"
./ragabast query "How do I install the system?" --verbose
```

#### Search Documents

```bash
./ragabast search "chunking strategy"
./ragabast search "embedding" --doc-id "sample-doc-001"
```

#### Check System Status

```bash
./ragabast status
./ragabast status --verbose
```

## Architecture

### Document Processing

The system uses a three-stage pipeline:

1. **Parsing**: Extract YAML frontmatter and markdown content
2. **Chunking**: Split by H1/H2 headers with context preservation
3. **Embedding**: Generate vectors using Ollama API

### Vector Storage

Chunks are stored in chromem-go with:
- Hierarchical metadata (header paths, levels)
- Document context (title, fingerprint, UID)
- Similarity search capabilities

## Advanced Features

### Hierarchical Context

The chunker preserves document structure:
- H1 headers create root chunks
- H2 headers create child chunks
- Content inherits parent context

### Custom Frontmatter

Required fields:
- `uid`: Unique identifier
- `urls`: At least one URL
- `tags`: For categorization
- `categories`: Hierarchical classification

## Web Interface

The web interface provides:
- Responsive design with Bulma CSS
- HTMX for dynamic interactions
- Real-time search and query
- Document management

## API Endpoints

The REST API includes:
- `/api/search` - Semantic search
- `/api/query` - Natural language queries
- `/api/documents` - Document management
- `/api/status` - System health

## Configuration

Default configuration includes:
- Ollama: http://localhost:11434
- Embedding: nomic-ai/nomic-embed-text-v1.5
- Generation: gemma:2b
- Vector DB: ./data/vectors

## Examples

### Example 1: Ingest and Query

```bash
# Ingest a document
./ragabast ingest ./sample.md

# Query the system
./ragabast query "What are the main features?"
```

### Example 2: Search with Filters

```bash
# Search within specific document
./ragabast search "installation" --doc-id "sample-doc-001"

# Search with result count
./ragabast search "architecture" --top-k 10
```

## Troubleshooting

### Ollama Not Running

Ensure Ollama is running:
```bash
ollama serve
```

### Missing Models

Pull required models:
```bash
ollama pull nomic-ai/nomic-embed-text-v1.5
ollama pull gemma:2b
```

### Vector DB Issues

Check permissions on data directory:
```bash
chmod 755 ./data/vectors
```

## Performance Tips

1. **Batch Processing**: Ingest multiple files at once
2. **Connection Pooling**: Reuse HTTP clients
3. **Caching**: Cache frequent queries
4. **Indexing**: Optimize chunk sizes

## Security Considerations

- Validate all input documents
- Sanitize frontmatter fields
- Rate limit API endpoints
- Monitor resource usage

## Future Enhancements

- [ ] Multi-tenant support
- [ ] Advanced chunking strategies
- [ ] Hybrid search (keyword + semantic)
- [ ] Real-time collaboration
- [ ] Export/import functionality

## Contributing

We welcome contributions! Please see AGENTS.md for guidelines.

## License

MIT License - see LICENSE file for details.

## Support

For issues and questions:
- GitHub Issues: https://github.com/ragabast/issues
- Documentation: https://ragabast.dev/docs