# RAG/LLM System Architecture

## System Overview

```mermaid
graph TB
    subgraph "CLI Interface"
        CLI[ragabast CLI]
        ingest[ingest command]
        query[query command]
        serve[serve command]
        status[status command]
        list[list command]
    end

    subgraph "Document Processing"
        parser[Docubilder Parser]
        chunker[Chunking Engine]
        validator[Validation Layer]
    end

    subgraph "Vector Database"
        chromem[chromem-go]
        embeddings[Embedding Store]
        metadata[Metadata Store]
    end

    subgraph "LLM Integration"
        ollama[Ollama API]
        embed_model[nomic-embed-text-v1.5]
        gen_model[gemma:2b]
    end

    subgraph "Web Backend"
        server[HUMA v2 Server]
        api[REST API]
        web_handlers[Web UI Handlers]
        rag_endpoint[RAG Query Handler]
    end

    subgraph "Web Frontend"
        templates[Go Templates]
        htmx[HTMX]
        bulma[Bulma CSS]
        upload[Upload Interface]
        chat[Chat Interface]
    end

    CLI --> parser
    CLI --> query
    CLI --> serve
    
    parser --> validator
    parser --> chunker
    
    chunker --> chromem
    chromem --> embeddings
    chromem --> metadata
    
    embeddings --> ollama
    ollama --> embed_model
    
    serve --> server
    server --> api
    server --> web_handlers
    server --> rag_endpoint
    
    web_handlers --> templates
    templates --> htmx
    templates --> bulma
    
    upload --> parser
    chat --> rag_endpoint
    
    rag_endpoint --> ollama
    ollama --> gen_model
    rag_endpoint --> chromem
    
    query --> chromem
```

## Data Flow

1. **Document Ingestion**: 
   - Docubilder Markdown → Parser → Chunker → Embeddings → Vector DB

2. **Query Processing**:
   - User Query → Embedding → Vector Search → Context Retrieval → LLM Generation → Response

3. **Web Interface**:
   - Browser → HTMX → Server → API/LLM → Templates → Browser

## Component Responsibilities

- **Models**: Data structures and types
- **Parser**: Frontmatter extraction and Markdown parsing
- **Chunker**: H1/H2-based splitting with context preservation
- **Vector DB**: chromem-go wrapper for storage and search
- **LLM Client**: Ollama API integration for embeddings and generation
- **CLI**: Command-line interface for all operations
- **Server**: HTTP server with REST API and web UI
- **Templates**: HTMX-powered responsive frontend