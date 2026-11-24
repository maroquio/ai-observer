# HTTP Inspector — guia rápido

O HTTP Inspector mostra o tráfego **real** entre o Claude Code e a API da
Anthropic: cada request e response, o `system` prompt e os schemas de
ferramentas que foram de fato enviados, os eventos SSE com seus tempos,
`count_tokens`, uso de tokens e cache (5m/1h) e o encadeamento
`tool_use → tool_result`.

Ele funciona como um **proxy reverso local**: o Claude Code envia as
requests para `ANTHROPIC_BASE_URL` (o proxy), que as repassa sem alteração
para `https://api.anthropic.com` e registra o que observou. É uma fonte
separada do OTLP e da importação de arquivos de sessão: nada do que o proxy
registra é somado às métricas existentes.

> O proxy vem **desligado**. Ligado, escuta só em `127.0.0.1`, encaminha
> apenas para o upstream fixo e nunca grava `Authorization`, `x-api-key` ou
> cookies.

## 1. Instalar / compilar

```bash
make setup      # dependências (Go 1.26, Node 22, pnpm 10)
make backend    # frontend embutido + binário em bin/ai-observer
```

### Com Docker (sem Go nem Node na máquina)

Pré-requisito: Docker Desktop (ou Docker Engine). Na raiz do repositório:

```bash
docker compose up -d --build     # compila a imagem e sobe o servidor com o proxy ligado
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
claude
```

- A primeira compilação leva alguns minutos e baixa bastante coisa
  (imagens do Go e do Node, dependências): faça antes da aula, não no
  Wi-Fi da sala. A imagem sai para a arquitetura da máquina (arm64 num Mac
  Apple Silicon, amd64 em Intel e na maioria dos PCs Windows).
- Painel: `http://127.0.0.1:8080/http-inspector`. Parar:
  `docker compose down` (os dados ficam no volume `ai-observer-data`;
  `docker compose down -v` os apaga).
- Windows: PowerShell `$env:ANTHROPIC_BASE_URL="http://127.0.0.1:8787"`;
  cmd `set ANTHROPIC_BASE_URL=http://127.0.0.1:8787`.
- Porta ocupada: troque com a variável de mesmo nome, por exemplo
  `AI_OBSERVER_HTTP_PROXY_PORT=8788 docker compose up -d` (também
  `AI_OBSERVER_API_PORT`, `_OTLP_PORT`, `_OTLP_GRPC_PORT`), e use a porta
  nova no `ANTHROPIC_BASE_URL`. Dentro e fora do contêiner a porta é a
  mesma, então os endereços mostrados no painel continuam certos.
- Telemetria OTLP (opcional, para a comparação proxy × telemetria): as
  variáveis do Claude Code apontam para `http://127.0.0.1:4318`
  (`CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_LOGS_EXPORTER=otlp`,
  `OTEL_METRICS_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`,
  `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`).
- **`ai-observer run -- claude` não funciona com o contêiner:** cada
  execução abriria uma porta própria dentro dele, inalcançável do host.
  Com Docker vale só o modo manual (seção 3): todas as requests entram na
  execução `manual` do servidor, uma por vez que o contêiner inicia.
- As portas são publicadas só em `127.0.0.1`. Dentro do contêiner o proxy
  escuta em todas as interfaces (`AI_OBSERVER_HTTP_PROXY_HOST=0.0.0.0` com
  `AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE=true`, definidos no
  `compose.yaml`), alcançável apenas pela porta publicada.

## 2. Modo launcher (recomendado)

Um terminal com o servidor:

```bash
ai-observer serve --http-proxy
```

Outro terminal, no projeto em que você vai usar o Claude Code:

```bash
ai-observer run -- claude
# ou com nome e argumentos:
ai-observer run --name "Aula 3" -- claude -p "Explique este repositório"
```

O `run` cria uma **execução de captura** com uma porta só dela, inicia o
programa com `ANTHROPIC_BASE_URL` apontando para essa porta e não muda mais
nada do ambiente: o login do Claude Code (assinatura claude.ai ou
`ANTHROPIC_API_KEY`) passa intacto. Ao terminar ele imprime:

```
AI Observer: captura "Aula 3", proxy http://127.0.0.1:53817
...
AI Observer: 7 trocas capturadas — http://127.0.0.1:8080/http-inspector/runs/<id>
```

Sem servidor rodando, `ai-observer run --serve -- claude` sobe um servidor
só para aquela execução (log em `data/ai-observer-run.log`). Depois, para
ver a captura, inicie `ai-observer serve` e abra o link impresso.

## 3. Modo manual (porta compartilhada)

```bash
ai-observer serve --http-proxy                  # porta compartilhada 8787
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
claude
```

Todas as requests que chegam pela porta compartilhada entram numa execução
`manual`, criada na primeira request. Use o launcher quando quiser separar
execuções paralelas.

## 4. Onde ver

- **Sessões → sessão → aba "Tráfego da API"**: resumo, timeline com cada
  exchange (inclusive `count_tokens`, erros e outros endpoints) e o
  inspector com as abas Resumo, Request, Response, SSE Stream,
  Tokens & Cache e Context Diff. Link direto:
  `/sessions/<sessão>/http-exchanges/<exchange>`.
- **HTTP Inspector** (menu): status do proxy, execuções de captura, ferramentas
  (`tool_use → tool_result`), renomear, exportar (zip saneado) e apagar.
  Link direto: `/http-inspector/runs/<execução>`.
- A lista de sessões mostra quantas trocas foram capturadas em cada sessão.

A sessão vem do cabeçalho `X-Claude-Code-Session-Id` (ou, na falta dele, de
`metadata.user_id`), o mesmo ID usado pelo OTLP e pelos arquivos de sessão.

## 5. Modos de captura

| `AI_OBSERVER_HTTP_CAPTURE_MODE` | O que é gravado |
|---|---|
| `safe` (padrão) | corpos com segredos mascarados (`[REDACTED:...]`), headers saneados |
| `metadata_only` | só metadados, uso, eventos e resumos; nenhum corpo |
| `full_raw` | também uma cópia **sem redação** dos corpos (opt-in explícito, local) |

Credenciais nos headers nunca são gravadas em nenhum modo.

## 6. Configuração

| Variável | Padrão | Uso |
|---|---|---|
| `AI_OBSERVER_HTTP_PROXY_ENABLED` | `false` | liga o proxy (`--http-proxy`) |
| `AI_OBSERVER_HTTP_PROXY_HOST` | `127.0.0.1` | interface; outra exige `AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE=true` |
| `AI_OBSERVER_HTTP_PROXY_PORT` | `8787` | porta compartilhada (`--http-proxy-port`); `off` = só launcher |
| `AI_OBSERVER_HTTP_PROXY_UPSTREAM` | `https://api.anthropic.com` | destino fixo: https, ou http só para loopback (testes) |
| `AI_OBSERVER_HTTP_CAPTURE_MODE` | `safe` | `safe`, `metadata_only`, `full_raw` |
| `AI_OBSERVER_HTTP_CAPTURE_DIR` | `http-captures` ao lado do banco | corpos gravados |
| `AI_OBSERVER_HTTP_CAPTURE_MAX_BODY_BYTES` | `32MB` | maior corpo gravado (o tráfego segue inteiro) |
| `AI_OBSERVER_HTTP_CAPTURE_QUEUE_SIZE` | `1024` | registros aguardando gravação |
| `AI_OBSERVER_HTTP_CAPTURE_RETENTION_DAYS` | `AI_OBSERVER_RETENTION_DAYS` | apaga execuções antigas |
| `AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT` | `2GB` | apaga as execuções mais antigas acima do limite (`off` desliga) |

`ai-observer --help` lista todas; `ai-observer setup claude-code` mostra OTLP
e HTTP Inspector lado a lado (são configurações diferentes:
`OTEL_EXPORTER_OTLP_ENDPOINT` ≠ `ANTHROPIC_BASE_URL`).

## 7. Testar sem chave e sem custo

```bash
make backend fake-anthropic                      # bin/ai-observer e bin/fake-anthropic
cd docs/http-inspector/demo-project
../../../bin/fake-anthropic --listen 127.0.0.1:9787 --file "$PWD/src/auth.ts" &
AI_OBSERVER_HTTP_PROXY_UPSTREAM=http://127.0.0.1:9787 ../../../bin/ai-observer serve --http-proxy &
ANTHROPIC_API_KEY="chave-ficticia" ../../../bin/ai-observer run -- claude -p "Revise src/auth.ts" --allowedTools Read
```

O upstream simulado responde de forma determinística (veja
[TEACHING_DEMO.md](TEACHING_DEMO.md)); nada vai para a API real.

Mais: [ARCHITECTURE.md](ARCHITECTURE.md) · [SECURITY.md](SECURITY.md) ·
[TROUBLESHOOTING.md](TROUBLESHOOTING.md) · [TESTING.md](TESTING.md).
