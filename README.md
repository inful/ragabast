# ragabast

docbuilder markdown chunker and rag/llm information retriever

## Configuration

ragabast reads configuration from YAML.

Precedence:
- `--config <path>` (per-command)
- `RAGABAST_CONFIG` env var (equivalent to `--config`)
- `./config.yml`
- `./config.yaml`
- built-in defaults (with env var overrides like `OLLAMA_BASE_URL`)

See [config.example.yml](config.example.yml) for a starting point.

Notes:
- Set `ollama.temperature` in YAML (or `OLLAMA_TEMPERATURE`) to control sampling.
- `ragabast query --temperature ...` overrides config/env for that invocation.
- The chat completions server (`ollama.chat_base_url` / `ollama.chat_model`) speaks
  the OpenAI Chat Completions API. Works against Ollama 0.5+, vLLM, llama.cpp
  `--server`, LM Studio, llama-stack, OpenRouter, and OpenAI itself.
- The embeddings server (`ollama.base_url` / `ollama.embedding_model`) speaks
  the OpenAI Embeddings API. Works against Ollama 0.5+ (with the
  `nomic-embed-text` image), vLLM, llama.cpp `--embedding`, LM Studio, OpenAI,
  and Google's Generative AI API via its OpenAI-compat layer
  (`https://generativelanguage.googleapis.com/v1beta/openai`). Use the actual
  Google embedding model names: `gemini-embedding-001` (text, 768 dims) or
  `gemini-embedding-2` (multimodal, 3072 dims by default; the latest). The
  bearer-token auth flow is identical to OpenAI's, so `ollama.embedding_api_key`
  just takes your `GEMINI_API_KEY`.
- Use `ollama.api_key` as the default bearer token for both servers. Set
  `ollama.chat_api_key` and/or `ollama.embedding_api_key` (env:
  `OLLAMA_CHAT_API_KEY`, `OLLAMA_EMBEDDING_API_KEY`) when the chat and
  embeddings providers require different tokens.
- Changing `embedding_model` (or its `EmbeddingDimension` in `vectordb:`) requires
  re-ingesting all documents: stop the server, `rm -rf data/vectors/`, and run
  `ragabast ingest` again.
- Set `ragabast.docbuilder_base_url` in YAML (or
  `RAGABAST_DOCBUILDER_BASE_URL`, e.g.
  `https://docs.example.com`) to surface a synthetic
  permalink for every search result. ragabast computes
  `<base>/_uid/<uid>/` from each document's UID frontmatter
  and exposes it as `docbuilder_url` in API responses, the
  link-suggestions endpoint, the web UI, and the chat prompt
  so the LLM can cite a stable direct link. The `/_uid/` alias
  is docbuilder's stable permalink convention: derived from
  the frontmatter UID rather than the file path, so downstream
  indexers, bookmarks, and external links stay valid even
  after the doc moves on disk. Empty by default, which means
  only the doc's own frontmatter `urls:` surface as links.
- Set `ollama.embedding_dimensions` in YAML (or `OLLAMA_EMBEDDING_DIMENSIONS`)
  to request Matryoshka truncation from the embeddings server. Useful for
  `jina-embeddings-v5-text-small` (supported: 32, 64, 128, 256, 512, 768,
  1024) and OpenAI `text-embedding-3-*` (any positive integer). Leave at 0 to
  disable truncation; the field is omitted from the request body when unset so
  older Ollama versions don't reject it. When set, the client logs a one-shot
  `DIMENSION MISMATCH` warning if the server returns vectors of a different
  length than configured. **Not supported by Google Gemini** — the
  OpenAI-compat layer silently ignores the field; if you set
  `embedding_dimensions: N > 0` against Google you'll get a
  `DIMENSION MISMATCH` warning on every request. Leave at 0. To get a
  different dimension out of Google's `gemini-embedding-2` model, use the
  native endpoint (`/v1beta/models/gemini-embedding-2:embedContent`) with
  the `outputDimensionality` request field — not currently wired through
  ragabast.

## Usage

- Start the web server: `ragabast serve`
- Use a specific config file: `ragabast serve --config ./config.yml`
- Create a starter config: `ragabast config init` (or `ragabast init`) (writes `config.yml`)
