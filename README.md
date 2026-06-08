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
  `nomic-embed-text` image), vLLM, llama.cpp `--embedding`, LM Studio, and OpenAI.
- Use `ollama.api_key` as the default bearer token for both servers. Set
  `ollama.chat_api_key` and/or `ollama.embedding_api_key` (env:
  `OLLAMA_CHAT_API_KEY`, `OLLAMA_EMBEDDING_API_KEY`) when the chat and
  embeddings providers require different tokens.
- Changing `embedding_model` (or its `EmbeddingDimension` in `vectordb:`) requires
  re-ingesting all documents: stop the server, `rm -rf data/vectors/`, and run
  `ragabast ingest` again.

## Usage

- Start the web server: `ragabast serve`
- Use a specific config file: `ragabast serve --config ./config.yml`
- Create a starter config: `ragabast config init` (or `ragabast init`) (writes `config.yml`)
