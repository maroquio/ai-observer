# HTTP Inspector — modelo de dados

Tudo o que o proxy observa fica em **seis tabelas próprias** no mesmo
DuckDB do AI Observer, mais os **corpos** em arquivos no diretório de
captura. Nenhuma linha vai para `otel_logs`, `otel_metrics` ou
`otel_traces`.

Definição: `backend/internal/storage/http_capture_schema.go`.

## Migração

As tabelas fazem parte do **schema base** (`CREATE TABLE IF NOT EXISTS`,
executado a cada início, idempotente). Não há migração numerada: a versão
de schema continua a do upstream (`LatestSchemaVersion`), o banco continua
abrindo no AI Observer original, e nenhum backup pré-migração é criado.
Colunas acrescentadas no futuro entram na lista ordenada de migrações.

## Tabelas

### `http_capture_runs` — execuções de captura

| Coluna | Significado |
|---|---|
| `run_id` | UUIDv7 |
| `name`, `note` | nome (padrão "Execução …") e nota editáveis |
| `mode` | `manual` (porta compartilhada) ou `launcher` (`ai-observer run`) |
| `state` | `active`, `finished`, `interrupted` |
| `capture_mode` | `safe`, `metadata_only`, `full_raw` no momento da captura |
| `host`, `port`, `upstream` | onde o proxy escutou e para onde encaminhou |
| `cwd`, `command` | diretório e linha de comando do launcher (redigida) |
| `pid`, `exit_code` | do programa iniciado pelo launcher |
| `started_at`, `ended_at`, `server_version` | |

### `http_capture_exchanges` — um request e sua response

Identidade e roteamento: `exchange_id` (UUIDv7), `run_id`, `seq` (ordem de
chegada na execução), `session_id` + `session_evidence`, `request_id`
(cabeçalho `request-id` da resposta), `method`, `path`, `query`, `kind`
(`model_inference`, `token_count`, `other_api`, `transport_error`).

Estado: `state` (`in_progress`, `complete`, `partial`, `aborted`,
`failed`, `interrupted`: o servidor parou durante a troca), `status` (HTTP recebido pelo cliente, 0 = nenhum),
`degraded` + `degraded_reasons` (partes não capturadas; o tráfego não foi
afetado), `error_type`, `error_message`.

Modelo e resposta: `requested_model`, `response_model`, `stream`,
`stop_reason`, `message_id`, `retry_count` (`X-Stainless-Retry-Count`).

Tempos (ms desde o início): `duration_ms`, `ttfb_ms`, `first_event_ms`.
Tamanhos: `request_bytes`, `response_bytes` (no fio), `sse_events`.

Semântica: `headline` (frase determinística), `observations` (frases com
evidência), `tool_uses`, `tool_results`, `request_summary` (resumo
estruturado: `system`, `tools`, `messages`, breakpoints, parâmetros),
`response_message` (mensagem reconstruída), `first_message_hash`
(conversa, usado pelo Context Diff), `lifecycle` (eventos de transporte com
horário: `request_started`, `upstream_connected`,
`response_headers_received`, `response_streaming`, `completed`,
`upstream_error`, `client_aborted`, `upstream_aborted`, `capture_degraded`,
`stream_parse_error`).

Headers: `request_headers`, `response_headers` **saneados** (credenciais
substituídas por `[REDACTED]` antes de qualquer gravação),
`*_content_type`, `*_content_encoding`.

### `http_capture_sse_events` — cada evento SSE

`exchange_id`, `seq`, `offset_ms` (tempo de chegada relativo ao início),
`byte_offset`, `byte_len`, `event`, `type`, `block_index`, `delta_type`,
`parse_status`, `note`, `data` (o JSON do evento, saneado; vazio em
`metadata_only`), `data_bytes`, `truncated`. Eventos desconhecidos também
são gravados.

### `http_capture_usage` — tokens e custo por exchange

| Coluna | Significado |
|---|---|
| `input_tokens`, `output_tokens` | da API; `output` é o **último valor cumulativo** do stream, nunca a soma |
| `cache_read_tokens`, `cache_write_tokens` | `cache_read_input_tokens`, `cache_creation_input_tokens` |
| `cache_write_5m`, `cache_write_1h` | `cache_creation.ephemeral_5m/1h_input_tokens` |
| `cache_write_unknown` | escrita de cache sem divisão informada |
| `total_input`, `total_processed` | input + cache read + cache write; + output |
| `count_tokens` | `input_tokens` de uma resposta de `count_tokens`: estimativa, **não** inferência |
| `quality` | `final` (resposta completa), `partial` (stream interrompido: último valor visto), `unavailable` |
| `raw_usage`, `snapshots` | o objeto `usage` original e sua evolução evento a evento |
| `cost_usd`, `cost_status`, `cost_reason`, `priced_model`, `cost_components` | custo equivalente de API pelo catálogo de preços (`complete`, `partial`, `unavailable`) |
| `source` | sempre `http_proxy` |

`NULL` significa "não informado"; `0` é zero informado.

### `http_capture_tool_links` — `tool_use → tool_result`

Chave `(run_id, tool_use_id)`: `name`, `session_id`, `use_exchange_id`,
`use_seq`, `use_count`, `input`, `result_exchange_id`, `result_seq`,
`result_is_error`, `result_preview`, `result_bytes`. O status é calculado
na consulta: `matched`, `pending` (execução ativa), `unresolved` (execução
terminada sem resultado), `ambiguous` (o mesmo ID pedido mais de uma vez).

### `http_capture_payloads` — referências aos corpos

Chave `(exchange_id, part, variant)`; `part` = `request`/`response`,
`variant` = `safe`/`raw`. `rel_path` (relativo ao diretório de captura),
`observed_bytes` e `observed_sha256` (bytes no fio, codificados; SHA vazio
quando houve perda), `stored_bytes` e `stored_sha256` (variante gravada,
decodificada), `content_encoding`, `decode_status`, `truncated`,
`sanitized`, `redactions`.

## Corpos em arquivos

```
<AI_OBSERVER_HTTP_CAPTURE_DIR>/            (0700; padrão: http-captures ao lado do banco)
  runs/<run_id>/<exchange_id>.request.safe   (0600)
  runs/<run_id>/<exchange_id>.response.safe
  runs/<run_id>/<exchange_id>.request.raw    (só full_raw)
  tmp/                                       (spools do full_raw; limpo no início)
```

- Acesso por `os.Root`: nenhum caminho sai do diretório; IDs são validados.
- Gravação atômica (arquivo temporário + rename).
- `safe`: corpo decodificado (gzip/deflate/br/zstd) e saneado.
- `raw`: os bytes decodificados sem redação, só em `full_raw`.
- `metadata_only`: nenhum arquivo.
- Corpos acima de `AI_OBSERVER_HTTP_CAPTURE_MAX_BODY_BYTES` são gravados
  cortados (`truncated`); o tráfego segue inteiro.

## Qualidade e origem dos dados

| Dado | Origem | Rótulo na interface |
|---|---|---|
| request, response, SSE, headers | fio (`wire`) | `wire` |
| sessão | cabeçalho ou `metadata.user_id` | evidência gravada |
| uso | `usage` da resposta (último cumulativo) | `final` / `partial` / `unavailable` |
| custo | catálogo de preços × uso | `derivado`, com status |
| frases, tool links, diff | regras determinísticas sobre o fio | `derivado`, com evidência |
| comparação com OTLP/JSONL | `request_id` | `otel` / `jsonl`, certeza |

## Retenção e exclusão

- `AI_OBSERVER_HTTP_CAPTURE_RETENTION_DAYS` (padrão:
  `AI_OBSERVER_RETENTION_DAYS`): o ticker horário apaga execuções terminadas
  há mais tempo.
- `AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT` (padrão 2 GB): enquanto os corpos
  passam do limite, apaga as execuções terminadas mais antigas.
- `DELETE /api/http-inspector/runs/{id}` apaga uma execução (409 se ativa):
  linhas das seis tabelas numa transação, depois os arquivos. Diretórios de
  corpos sem execução no banco são removidos no início do servidor.
- `ai-observer delete`, retenção de telemetria e o limite
  `AI_OBSERVER_MAX_DB_SIZE` **não** apagam capturas.

## Backup e export

- `GET /api/http-inspector/runs/{id}/export`: zip com `manifest.json`,
  `run.json`, `tools.json` e `exchanges/NNNN-<id>.json` com os corpos
  **saneados** apenas (nunca a variante raw).
- `ai-observer backup` copia o banco inteiro (inclusive as tabelas de
  captura), **não** os arquivos de corpos: copie também o diretório de
  captura.
- `ai-observer export`/`restore` (Parquet) não incluem capturas.

## Índices

Só chaves primárias. Exchanges são lidos por ID, execuções por ID, tool
links por `(run_id, tool_use_id)`; eventos SSE são inseridos em ordem por
exchange, então os zonemaps do DuckDB atendem às leituras por
`exchange_id` sem um índice ART (que, nas tabelas de telemetria, deixou o
banco ~13x maior sem ganho medido).
