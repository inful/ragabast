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

## Usage

- Start the web server: `ragabast serve`
- Use a specific config file: `ragabast serve --config ./config.yml`
- Create a starter config: `ragabast config init` (or `ragabast init`) (writes `config.yml`)
