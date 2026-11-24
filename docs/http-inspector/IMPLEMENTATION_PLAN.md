# HTTP Inspector — plano de implementação e checklist

Fork: `maroquio/ai-observer` · branch `feature/anthropic-http-inspector`
Base upstream: `tobilg/ai-observer@7d56a16b1de5f2d20c841a776a0a06c7d7c17621` (v0.8.0, MIT)

Este arquivo é o checklist vivo da implementação. Cada item marcado foi
verificado por teste automatizado ou por execução registrada em
[TESTING.md](TESTING.md).

## Baseline (antes de qualquer alteração)

- `go build ./...` — ok.
- `go test ./... -count=1` — 31 pacotes ok, 1 falha **pré-existente**:
  `internal/otlp TestNanosToTime`, dependente do fuso horário local
  (falha com `TZ=America/Sao_Paulo`, passa com `TZ=UTC`). Não foi alterada.
- Frontend: `pnpm test:run` — 75 arquivos, 654 testes ok; `pnpm lint` ok;
  `pnpm build` ok.

## Reconhecimento do tráfego real (sem chave, sem custo)

Claude Code `2.1.331` executado com `claude -p` contra um upstream falso
local, `CLAUDE_CONFIG_DIR` isolado e chave fictícia gerada em tempo de
execução (a captura não foi versionada):

- caminho `POST /v1/messages?beta=true` (classificação usa o *path*, não a URL);
- cabeçalho `X-Claude-Code-Session-Id: <uuid>` em cada request;
- `metadata.user_id` é uma *string JSON* com `session_id` igual ao cabeçalho;
- `Accept-Encoding: gzip, deflate, br, zstd` → o parser precisa decodificar
  brotli (dependência `github.com/andybalholm/brotli`, já presente no `go.sum`);
- `X-Stainless-Retry-Count` indica tentativas do cliente;
- `tools` com `defer_loading` (tool search ativo mesmo com `ANTHROPIC_BASE_URL`);
- corpo do request não comprimido.

## Fases

### Fase 0 — Reconhecimento e fork
- [x] Licença MIT e HEAD do upstream confirmados.
- [x] Fork criado; remotes `origin` (fork) e `upstream` (push desabilitado).
- [x] Branch dedicada criada a partir da base upstream.
- [x] Testes-base executados e registrados (acima).
- [x] Mapeados schema, migrações, gerador de tipos, sessões, pricing,
      WebSocket, rotas, configuração e CI.
- [x] Plano e ADRs iniciais.

### Fase 1 — Proxy + captura fundamental
- [x] `internal/httpproxy`: reverse proxy Go, upstream fixo validado, loopback.
- [x] Captura por *tee* não bloqueante com orçamento de bytes e fila limitada.
- [x] Streaming SSE comprovado por teste com barreira (`TestStreamingIsNotBuffered`).
- [x] Falha de armazenamento não interrompe o tráfego (`TestStoreFailureKeepsTraffic`, `TestBlobFailureMarksDegraded`).

### Fase 2 — DuckDB e histórico
- [x] Tabelas `http_capture_*` no schema base (idempotente).
- [x] Blobs locais `0700/0600`, SHA-256, sem path traversal (`os.Root`).
- [x] Runs `manual` e `launcher` (`ai-observer run -- claude`), porta exclusiva.
- [x] Persistência pós-reinício e recuperação de exchanges interrompidos.
- [x] Retenção, limite de armazenamento, exclusão e export saneado (`TestHTTPCaptureRetention`, E2E).

### Fase 3 — Parsers e semântica
- [x] `internal/anthropicwire`: SSE incremental, reconstrução, usage, request.
- [x] Usage/cache 5m/1h, qualidade final/parcial/ausente, `count_tokens` separado.
- [x] Correlação `tool_use` → `tool_result`, classificação, Context Diff.
- [x] Custo via catálogo de preços existente; sem dupla contagem entre fontes.

### Fase 4 — API e UI
- [x] Endpoints `/api/http-inspector/*` e `/api/sessions/{id}/http-exchanges`.
- [x] Tipos gerados (`make api-types`) e OpenAPI.
- [ ] Aba "Tráfego da API" na sessão, timeline e inspector com 6 abas.
- [x] Atualização ao vivo via WebSocket existente (mensagem `http_exchanges`; consumo na UI: ver Fase 4 UI).

### Fase 5 — Segurança, documentação e entrega
- [x] Revisão de ameaças (XSS, vazamento de headers, open proxy/SSRF, traversal) — [SECURITY.md](SECURITY.md).
- [x] Documentação em `docs/http-inspector/`, README e CHANGELOG.
- [x] Suite completa, `-race`, lint, build e E2E keyless — [TESTING.md](TESTING.md).
- [x] Verificação com o Claude Code 2.1.331 real contra upstream simulado (sem custo).
- [ ] Push da branch para o fork (aguardando autorização do usuário).

## Definição de pronto (seção 17)

| Item | Estado | Evidência |
|---|---|---|
| D01 Fork | ok | `maroquio/ai-observer` (origin); push para upstream desabilitado |
| D02 Branch | ok | `feature/anthropic-http-inspector`, commits por fatia |
| D03 Build | ok | `make backend` (frontend embutido) |
| D04 Compatibilidade | ok | `TestHTTPInspectorOff`; suíte original passa |
| D05 Forwarding | ok | `TestRequestBytesForwardedUnchanged`; chave chega ao upstream, não ao disco |
| D06 SSE streaming | ok | `TestStreamingIsNotBuffered` (barreira) |
| D07 Request raw | ok | variantes `safe`/`raw`, `TestSecretInBody` |
| D08 Response raw | ok | headers/status/corpo/stream/erros, `TestUpstreamErrorsPassThrough` |
| D09 System prompt | ok | resumo + corpo; verificado com Claude Code real (3 blocos, 29 KB) |
| D10 Tools | ok | 12 tools, `defer_loading`, sem truncamento silencioso (`truncated` explícito) |
| D11 SSE events | ok | todos os tipos, inclusive desconhecidos, com `offset_ms` |
| D12 Token counting | ok | `token_count`, separado da inferência |
| D13 Usage | ok | testes de uso cumulativo e totais |
| D14 TTL cache | ok | 5m/1h/unknown |
| D15 Custo | ok | catálogo; `partial`/`unavailable`, nunca 0 inventado |
| D16 Sem dupla contagem | ok | tabelas separadas; comparação por `request_id` |
| D17 Sessões | ok | evidência de sessão; execuções isoladas; histórico após reinício |
| D18 Tool flow | ok | vínculos `matched`/`pending`/`unresolved`/`ambiguous`, nota de execução local não observada |
| D19 Timeline | UI | ver Fase 4 |
| D20 Inspector | UI | ver Fase 4 |
| D21 Live updates | UI | WebSocket `http_exchanges` |
| D22 Privacidade | ok | headers nunca gravados; `safe` padrão; export saneado; loopback |
| D23 Robustez | ok | falhas de store/blob/parser não afetam o tráfego |
| D24 Retenção | ok | `TestHTTPCaptureRetention` |
| D25 Testes | ok | [TESTING.md](TESTING.md) |
| D26 Docs | ok | este diretório |
| D27 Changelog | ok | `[Unreleased]` |
| D28 Entrega | pendente | push e relatório final |

## Pendências conhecidas

- Login por assinatura (OAuth claude.ai) via `ANTHROPIC_BASE_URL` não verificado com tráfego real.
- SSE com `Content-Encoding` interpretado só ao final (sem tempo por evento).
- O custo de escrita de cache de 1 h fica `partial` enquanto o catálogo não tiver esse preço.
- Uma sessão vista só pelo proxy não aparece na lista de Sessões (que vem da telemetria); é acessível pela execução.
