# HTTP Inspector — arquitetura

O HTTP Inspector acrescenta ao AI Observer uma **terceira fonte** de dados do
Claude Code, ao lado do OTLP e dos arquivos de sessão (JSONL). As três
fontes ficam em tabelas separadas e são **comparadas**, nunca somadas.

## Três fontes, três caminhos

```mermaid
flowchart LR
    subgraph CC[Claude Code]
      direction TB
      OT[Exportador OTLP]
      JS[~/.claude/projects/*.jsonl]
      HTTP[Cliente HTTP da API]
    end

    OT -- "OTLP/HTTP :4318 · gRPC :4317" --> ING[handlers/otlp_ingest]
    JS -- "import / watch" --> IMP[importer · watcher]
    HTTP -- "ANTHROPIC_BASE_URL<br/>127.0.0.1:8787 ou porta do run" --> PX[httpproxy]
    PX -- "bytes inalterados" --> API[(api.anthropic.com)]

    ING --> TEL[(otel_logs · otel_metrics · otel_traces)]
    IMP --> TEL
    PX -. "cópia não bloqueante" .-> CAP[httpcapture]
    CAP --> HC[(http_capture_*)]
    CAP --> BL[[corpos em http-captures/]]

    TEL --> REST[REST /api/*]
    HC --> REST
    BL --> REST
    REST --> UI[Dashboard React]
    CAP -. "http_exchanges" .-> WS[WebSocket /ws] --> UI
```

- **OTLP** descreve o que o Claude Code *diz* sobre suas chamadas (eventos
  `api_request`, métricas de tokens e custo). Não contém os corpos HTTP.
- **JSONL** é o histórico local de mensagens, gravado pelo cliente.
- **HTTP Inspector** é o que passou **no fio**: requests e responses
  completos, cada evento SSE com tempo de chegada, `count_tokens`,
  chamadas auxiliares e erros.

## Pacotes

| Pacote | Papel |
|---|---|
| `internal/anthropicwire` | Parser puro do protocolo: classificação de endpoint, parser SSE incremental, reconstrução da mensagem, `usage` (cumulativo), resumo do request (`system`, `tools`, `messages`, breakpoints de cache, sessão), `tool_use`/`tool_result`, frases didáticas, Context Diff, decodificação gzip/deflate/br/zstd |
| `internal/httpproxy` | Reverse proxy (`httputil.ReverseProxy`) com upstream fixo validado, listener compartilhado e um listener por execução do launcher |
| `internal/httpcapture` | Observação não bloqueante dos corpos, worker por exchange, saneamento, gravação dos corpos, custo, vínculo de ferramentas e pipeline de escrita |
| `internal/httpturns` | Agrupa as trocas de uma sessão em turnos (dedução, sem I/O) e resume cada turno |
| `internal/storage/http_capture_*.go` | Tabelas, escrita em lotes e consultas (`http_capture_sessions.go`: lista de sessões e linhas para os turnos) |
| `internal/handlers/http_inspector.go` | API REST `/api/http-inspector/*` e `/api/sessions/{id}/http-exchanges` |
| `cmd/server/cmd_run.go` | `ai-observer run -- claude` |
| `cmd/fake-anthropic` | Upstream simulado para demonstrações e testes sem chave |

## Caminho de uma request

```mermaid
sequenceDiagram
    autonumber
    participant C as Claude Code
    participant P as httpproxy
    participant X as httpcapture (worker)
    participant A as api.anthropic.com
    participant W as Pipeline (escritor único)
    participant D as DuckDB + arquivos

    C->>P: POST /v1/messages?beta=true (x-api-key / Bearer)
    P->>X: Begin: exchange, sessão (X-Claude-Code-Session-Id)
    P->>A: mesmos bytes (Host do upstream, sem X-Forwarded-*)
    Note over P,X: o corpo do request é copiado em pedaços<br/>para o worker sem bloquear
    A-->>P: 200 text/event-stream
    P-->>C: cada evento repassado imediatamente (FlushInterval -1)
    P--)X: cópia de cada pedaço (canal limitado)
    X->>X: parser SSE incremental, tempo de chegada
    A-->>P: message_stop
    P->>X: End
    X->>X: reconstrói a mensagem, usage, custo, tool links, frases
    X->>W: registro final (fila não bloqueante)
    W->>D: lote de até 64 registros numa transação
    W--)D: notificação WebSocket http_exchanges ao dashboard
```

### Por que o tráfego nunca espera a captura

1. O proxy repassa os bytes ao cliente **antes** de entregá-los à captura.
2. A cópia para a captura é `select` com `default`: se o canal do exchange
   (512 pedaços) ou o orçamento global de memória (`BufferBytes`) estiver
   cheio, o pedaço é **descartado**, a parte é marcada como perdida e o
   exchange como `degraded`.
3. A gravação no banco é feita por um único goroutine com fila limitada
   (`AI_OBSERVER_HTTP_CAPTURE_QUEUE_SIZE`); fila cheia descarta o registro
   e conta em `droppedRecords` (self-metrics e `/api/http-inspector/status`).
4. Falhas de banco ou de disco viram `storeErrors` / `capture_degraded`,
   nunca erro para o cliente.

## Execuções (runs)

```mermaid
stateDiagram-v2
    [*] --> active: primeira request na porta compartilhada (manual)<br/>ou POST /runs (launcher)
    active --> finished: POST /runs/{id}/finish (fim do programa)
    active --> interrupted: servidor parou com a execução ativa<br/>(marcado no próximo início)
    finished --> [*]: DELETE / retenção / limite de armazenamento
    interrupted --> [*]: DELETE / retenção / limite de armazenamento
```

- **manual**: a porta compartilhada (`8787`). Uma execução por processo do
  servidor, criada na primeira request.
- **launcher**: cada `ai-observer run` recebe um listener `127.0.0.1:0`
  exclusivo; requests de execuções paralelas nunca se misturam.

## Sessões

A sessão de cada exchange vem, em ordem:

1. do cabeçalho `X-Claude-Code-Session-Id` (`header:X-Claude-Code-Session-Id`);
2. de `metadata.user_id` do corpo (`body:metadata.user_id`), que no Claude
   Code 2.1.x é uma string JSON `{"device_id":…,"account_uuid":…,"session_id":…}`
   e, em versões antigas, `user_<hex>_account_<uuid>_session_<uuid>`.

O ID é o mesmo que o OTLP (`session.id`) e os arquivos JSONL usam, por isso
a aba "Tráfego da API" aparece na sessão já existente. A evidência usada é
gravada e mostrada na interface.

## Turnos

O fio não tem ID de turno: `internal/httpturns` deduz os turnos das trocas
de uma sessão, em ordem de início, a partir do resumo do request
(`request_summary`: hash do `system`, hashes das mensagens, `lastUserIndex`,
índices dos `tool_result`, `prompt`).

- **Conversa principal**: inferências com ferramentas cuja `system` (sha256)
  ou primeira mensagem é a de uma request principal anterior; a primeira
  inferência com ferramentas inicia a conversa. Subagentes têm system prompt
  e primeira mensagem próprios; geração de título vai sem ferramentas; a sonda
  de cota tem `max_tokens` 1. Todas entram no turno em andamento.
- **Início de turno**: uma request principal cuja última mensagem do usuário
  (`lastUserIndex`, porque na primeira request do Claude Code 2.1 a última
  mensagem é uma mensagem `system` de ambiente) não tem `tool_result`, e que
  não é retry (`RetryCount > 0`, ou mesmo número de mensagens e mesmo hash da
  última que a request principal anterior).
- Trocas antes do primeiro turno formam o preâmbulo (índice 0).
- `prompt` são os blocos de texto da última mensagem do usuário que não
  começam com `<system-reminder>`, redigidos e cortados em 1.000 bytes; vazio
  em `metadata_only`. Capturas anteriores não têm `prompt` nem
  `lastUserIndex` (usa-se a última mensagem).
- Os números de cada turno seguem o resumo de tráfego: erros sem a sonda de
  cota, tokens e custo só de `model_inference`, uso parcial à parte.
- `GET /api/http-inspector/sessions` lista as sessões (as trocas sem sessão
  são a linha `sessionId: ""`) e `GET /api/http-inspector/sessions/{id}/turns`
  (`none` para as sem sessão) devolve resumo, execuções, comparação,
  preâmbulo e turnos de até 5.000 trocas (`truncated` além disso).

## Deduplicação entre fontes

- Tabelas separadas: `http_capture_*` não entram em `/api/stats`, nas
  séries de métricas, em `usage_summary` nem nos totais das sessões.
- `GET /api/sessions/{id}/http-exchanges` traz `comparison`: as requests do
  proxy são casadas com os eventos `api_request` do OTLP pelo `request_id`
  (cabeçalho `request-id` da resposta). O resultado diz quantas casaram e a
  certeza (`exact`, `partial`, `none`), com notas — os valores são
  mostrados lado a lado, **não somados**.
- Uso e custo do proxy têm `source: http_proxy`.

## Context Diff

Para cada request de inferência, a base de comparação é, em ordem:

1. a request anterior da **mesma conversa** (mesmo hash da primeira
   mensagem, calculado sem os `cache_control`, que o Claude Code move para a
   mensagem mais nova a cada chamada);
2. a anterior do mesmo modelo;
3. a anterior da sessão.

O diff mostra prefixo comum, mensagens adicionadas/substituídas, novos
`tool_result`, parâmetros alterados, hashes de `system` e `tools`,
ferramentas adicionadas/removidas e breakpoints de cache.

## Versões

- Testado com **Claude Code 2.1.331** (`claude-cli/2.1.331`) contra o
  upstream simulado, com chave fictícia (ver [TESTING.md](TESTING.md)).
- Esquema: tabelas novas no schema base (`CREATE ... IF NOT EXISTS`); a
  versão de schema do upstream não mudou, então o banco continua abrindo
  no AI Observer original (que apenas ignora as tabelas novas).

Decisões detalhadas: [adr/](adr/).
