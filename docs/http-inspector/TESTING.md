# HTTP Inspector — testes

Todos os testes rodam **sem chave e sem custo**: o upstream é um servidor
falso (`httptest` nos testes Go, `cmd/fake-anthropic` nas demonstrações).
Credenciais de teste são montadas em tempo de execução, nunca como
literais.

## Como executar

```bash
# Backend (do diretório backend/)
go test ./...                                   # suíte completa
go test -race ./... -timeout 300s               # com detector de corrida
go test -v ./internal/anthropicwire/            # parser do protocolo
go test -v ./internal/httpproxy/                # proxy + captura (fake upstream)
go test -v -run 'TestHTTPInspector|TestHTTPCapture' ./internal/server/   # E2E pelo servidor real
go test -v -run 'TestRunLauncher' ./cmd/server/ # launcher com processo filho
go test -run=^$ -fuzz=FuzzSSEParser -fuzztime=30s ./internal/anthropicwire/

# Frontend (do diretório frontend/)
pnpm lint && pnpm test:run && pnpm build

# Tudo
make test && make lint
```

`internal/otlp TestNanosToTime` falha com fuso diferente de UTC
**antes e depois** desta implementação (falha pré-existente do upstream);
passa com `TZ=UTC`.

## O que cada camada cobre

### Parser (`internal/anthropicwire`, 180 testes + 5 alvos de fuzz)

- SSE: evento dividido em **todas** as posições de byte, eventos em muitos
  pedaços, CRLF/CR na fronteira, comentários, linhas acima do limite,
  contabilidade de bytes, `Finish` com evento incompleto (AC-02).
- Reconstrução: texto, `tool_use` com `input_json_delta` fragmentado,
  thinking só quando transmitido, blocos desconhecidos, interrupções,
  índices fora de ordem, JSON parcial inválido.
- Uso cumulativo: output 1/10/20/31 → **31**, não a soma (AC-03); totais
  cache 100+200+300 e output 40 → input total 600, processado 640 (AC-04);
  zero ≠ ausente; 5m/1h/unknown.
- Request: `system`, `tools` (inclusive `defer_loading`), mensagens,
  breakpoints, `tool_result`, sessão de `metadata.user_id` nos dois
  formatos; requests de 10 MB (~42 ms).
- Hashes de mensagem sem `cache_control` (o breakpoint que o Claude Code
  move não quebra o prefixo comum).
- Frases didáticas (texto exato e evidência), Context Diff, decodificação
  gzip/deflate/br/zstd com limites.
- Fuzz: `FuzzSSEParser`, `FuzzParseRequest`, `FuzzParseMessageJSON`,
  `FuzzDecodeBody`, `FuzzJSONWalk` (25–45 s cada, milhões de execuções,
  sem falhas; a seed `testdata/fuzz/FuzzJSONWalk/7f71a2d116d5afde` é de um
  caso do harness corrigido).

### Proxy + captura (`internal/httpproxy`, 18 testes)

| Teste | Prova |
|---|---|
| `TestStreamingIsNotBuffered` | **barreira**: o cliente recebe e lê cada evento antes de o upstream enviar o próximo (D06) |
| `TestRequestBytesForwardedUnchanged` | corpo gzip e não comprimido, `defer_loading`, `tool_reference` e `anthropic-beta` chegam idênticos ao upstream (AC-01, AC-17) |
| `TestLargeBodyForwardedWholeStoredCut` | corpo acima do limite: encaminhado inteiro, gravado cortado |
| `TestCountTokensAndToolLinks` | `count_tokens` separado, `tool_use → tool_result` ligados (AC-05, AC-06) |
| `TestUpstreamErrorsPassThrough` | 401/403/429/500/529 repassados com corpo e status (AC-10) |
| `TestStreamErrorEventAndGzip` | evento `error` no meio do stream, SSE com gzip intacto |
| `TestUpstreamUnreachable` | 502 no formato da Anthropic, exchange `failed` |
| `TestRejectsBrowsersAndForeignHosts` | `Origin` e Host estranho → 403, `CONNECT` → 400 |
| `TestParallelClients`, `TestParallelLaunchers` | sem mistura entre clientes e execuções (AC-07) |
| `TestClientCancelMidStream`, `TestUpstreamResetMidStream` | `aborted`/`partial` corretos, uso `partial` |
| `TestStoreFailureKeepsTraffic`, `TestBlobFailureMarksDegraded` | banco/disco falhando: tráfego intacto, `degraded` (AC-11) |
| `TestOtherEndpointsRecorded` | outros endpoints registrados (`other_api`) (AC-20) |
| `TestSecretInBody` | `safe` mascara, `full_raw` guarda raw local, `metadata_only` não grava corpo (AC-13) |
| `TestUnpricedModel` | modelo sem preço: custo `unavailable`, nunca 0 (AC-14) |
| `TestValidateUpstream` | só https ou http loopback; sem caminho, query, credenciais |

### Servidor completo (`internal/server`)

- `TestHTTPInspectorEndToEnd`: servidor real (`server.New`), proxy real,
  upstream falso, sem chave. Cenários **A** (olá), **B** (tool use →
  resultado → resposta) e **C** (`count_tokens`, escrita e leitura de
  cache); todos os endpoints validados contra os tipos gerados; filtros,
  paginação por cursor, renomear, exportar, encerrar; a chave chega ao
  upstream mas não ao banco, arquivos ou export (AC-12); estatísticas de
  telemetria inalteradas pela captura e comparação por `request_id` com um
  evento OTLP (AC-08); reinício com a captura reaberta inteira (AC-09);
  exclusão de uma execução sem tocar na outra (AC-18); contador
  `httpExchanges` na lista de sessões.
- `TestHTTPInspectorOff`: proxy desligado, nenhuma porta nova, endpoints de
  leitura funcionando, criar execução → 409 (AC-16).
- `TestHTTPInspectorRejectsInvalidUpstream`.
- `TestHTTPCaptureRetention`: retenção por dias e limite de armazenamento
  apagam só execuções terminadas antigas, com seus arquivos (D24).
- Deriva de tipos/rotas: `TestAPIResponsesMatchTypes`,
  `TestRouteTableMatchesRouter`, `spec.TestGeneratedFilesAreUpToDate`.

### CLI (`cmd/server`)

- `TestRunLauncherCapturesTheProgram`: o binário de teste vira o
  "programa" (padrão helper process), faz uma request com chave fictícia
  pelo `ANTHROPIC_BASE_URL` recebido e sai com 3; o launcher devolve 3, a
  execução termina com `exitCode 3`, 1 troca, sessão do cabeçalho, linha de
  comando sem segredo.
- `TestRunLauncherServe`: `--serve` sobe o servidor só para a execução, log
  em arquivo; um segredo no prompt não aparece em nenhum arquivo; custo
  `complete`; um novo servidor reabre a captura.
- `TestHelpListsEverySetting`: todas as variáveis `AI_OBSERVER_HTTP_*`
  documentadas no `--help`.

### Upstream simulado (`cmd/fake-anthropic`)

`TestSimulatedToolUseConversation`: `tool_use` Read em streaming, resposta
final com leitura de cache, `count_tokens`, 404 para outros caminhos.

### Interface (`frontend/`, Vitest + Testing Library)

Suíte completa: **82 arquivos, 710 testes** (55 novos do inspector; os 655
anteriores continuam passando). Lint e build limpos.

| Arquivo | Prova |
|---|---|
| `components/http-inspector/__tests__/xss.test.tsx` (5) | **AC-15**: `<img onerror>`, `<script>` e `[x](javascript:…)` em request, response, eventos SSE e ferramentas aparecem como texto literal; nenhum `img`/`script`/`iframe`/`[src]` criado, `alert` nunca chamado; um session ID hostil (`javascript:alert(1)`) só gera links internos codificados |
| `pages/__tests__/HttpExchangePage.test.tsx` (15) | **AC-19**: exchange aberto direto pela URL (como após recarregar), pela rota da sessão e da execução, com a aba na URL; as seis abas (Resumo, Request, Response, SSE Stream, Tokens & Cache, Context Diff), navegação por teclado, valores mascarados marcados, corpo raw só com `full_raw`, ausência de usage, primeiro request sem diff |
| `components/http-inspector/__tests__/SessionTrafficTab.test.tsx` (8) | aba "Tráfego da API": resumo separado da comparação com a telemetria, todos os tipos de troca (inferência, `count_tokens`, outro endpoint, erro de transporte) com rótulos parcial/redigido/sem usage/não correlacionado, filtros e busca na URL |
| `components/http-inspector/__tests__/SseTab.test.tsx` (1) | stream de **5.000 eventos**: menos de 100 linhas renderizadas (virtualização) |
| `components/http-inspector/__tests__/ToolLinks.test.tsx` (3) | `tool_use → tool_result` com links para os dois exchanges; aviso de que a execução local não é observada |
| `pages/__tests__/HttpInspectorPage.test.tsx` (9) | lista de execuções e status do proxy, filtros na URL, link impresso pelo launcher, renomear, excluir (409 em execução ativa), execução inexistente, proxy desligado com instruções |
| `lib/httpInspector.test.ts` (9) | rótulos, caminhos (IDs codificados), formatação |
| `SessionTranscriptPage`, `SessionsPage`, `useWebSocket` (testes acrescentados) | aba de tráfego sem captura e de sessão vista só pelo proxy, contador `httpExchanges` na lista, mensagens `http_exchanges` do WebSocket |

As fixtures (`src/test/httpInspectorFixtures.ts`) são inventadas e não têm
strings com formato de credencial.

### Telas com tráfego real do Claude Code

As imagens em `docs/images/http-inspector-*.png` foram capturadas na
interface real com o Claude Code 2.1.331 contra o `fake-anthropic`
(respostas do modelo simuladas), num diretório neutro (`/tmp/aula-demo`),
com telemetria OTLP ligada: a comparação da sessão mostrou certeza
**exata** (2 de 2 `request_id`), proxy e telemetria com 6.624 tokens de
entrada / 66 de saída, sem soma. Essa verificação encontrou e corrigiu a
falta dos totais da telemetria na comparação (o lado "Telemetria" aparecia
como "não informado").


## Verificação com o Claude Code real (sem custo)

Executado em 2026-11-24 com **Claude Code 2.1.331**, `CLAUDE_CONFIG_DIR`
isolado, chave fictícia gerada na hora, upstream `fake-anthropic` em
loopback (nenhuma chamada à API real):

```bash
fake-anthropic --listen 127.0.0.1:18393 --file "$PROJ/src/auth.ts" &
AI_OBSERVER_HTTP_PROXY_UPSTREAM=http://127.0.0.1:18393 AI_OBSERVER_HTTP_PROXY_PORT=18392 \
  AI_OBSERVER_API_PORT=18390 ai-observer serve --http-proxy &
ai-observer run --name "Smoke Claude Code real" -- \
  claude -p "Revise o arquivo src/auth.ts e aponte um problema de segurança" \
  --allowedTools Read --model claude-sonnet-4-5-20250929
```

Resultado observado:

- launcher: `2 trocas capturadas`, execução `finished`, `exitCode 0`,
  sessão vinda de `X-Claude-Code-Session-Id`;
- #1: `POST /v1/messages?beta=true` 200, 20 eventos SSE, `stop_reason
  tool_use`, ferramenta `Read`, `system` com 3 blocos (29 KB), 12 `tools`
  (1 com `defer_loading`), cache write 5m 3000, custo `complete`;
- o Claude Code **executou o Read localmente** e enviou o `tool_result`;
- #2: 14 eventos SSE, `end_turn`, 1 `tool_result` novo; vínculo
  `toolu_sim_0001` **matched**; Context Diff `same_conversation`, prefixo
  comum 1, +2 mensagens;
- `X-Api-Key` gravado como `[REDACTED]`; `grep` pela chave no banco e no
  diretório de captura: 0 ocorrências;
- modo manual (`ANTHROPIC_BASE_URL=http://127.0.0.1:18392 claude -p …`)
  e `count_tokens` via `curl` também registrados; após reiniciar o servidor,
  as execuções reabriram inteiras.

Esta verificação encontrou e corrigiu um defeito que os testes sintéticos
não pegavam: o Claude Code move o `cache_control` para a mensagem mais nova
a cada request, o que mudava o hash da primeira mensagem e fazia o Context
Diff perder a conversa.

**Não verificado:** login por assinatura (OAuth claude.ai) através do
proxy, e tráfego contra a API real — ambos exigem conta e geram consumo.

## Limitações dos testes

- O navegador não é exercitado por testes E2E automatizados (Playwright
  não faz parte da suíte do projeto); a interface é testada com Vitest +
  Testing Library (jsdom) e foi conferida manualmente num navegador real
  (telas acima).
- SSE com `Content-Encoding` é testado quanto ao repasse íntegro; a
  interpretação ocorre ao final, sem tempo por evento.
