# HTTP Inspector — solução de problemas

Comece por `GET /api/http-inspector/status` (ou a página HTTP Inspector):
mostra se o proxy está ligado, porta, upstream, modo de captura, execuções
ativas, contadores (`exchanges`, `droppedRecords`, `storeErrors`,
`parseErrors`, `lostBodyBytes`) e avisos. Os contadores valem desde o
início do processo: depois de reiniciar o servidor eles voltam a 0, mesmo
com execuções antigas gravadas.

## O servidor não inicia: porta ocupada

```
starting HTTP capture proxy (set AI_OBSERVER_HTTP_PROXY_PORT to another port, or off): listen tcp 127.0.0.1:8787: bind: address already in use
```

Outro processo usa a porta compartilhada. Use outra
(`--http-proxy-port 8788`) ou `AI_OBSERVER_HTTP_PROXY_PORT=off` para usar
só o launcher (cada `ai-observer run` abre sua própria porta).

## `ai-observer run` diz que nenhum AI Observer responde

```
no AI Observer answers at http://127.0.0.1:8080: start it with `ai-observer serve --http-proxy` (or add --serve …)
```

Inicie o servidor com `--http-proxy`, aponte para ele com
`--server http://host:porta`, ou use `--serve`. Com o proxy desligado, criar
a execução responde `409`.

## O Claude Code recebe 401

O proxy não autentica nem altera credenciais: um `401` vem da API (chave
inválida/expirada, ou login que não vale para `ANTHROPIC_BASE_URL`). Teste
sem o proxy. O inspector mostra o `401` com `error_type`
`authentication_error`; o cabeçalho de credencial aparece só como
`[REDACTED]`.

## O Claude Code recebe 403 do proxy

- `requests from web pages (Origin header) are not accepted`: a request
  veio de um navegador. O proxy só aceita clientes sem `Origin`.
- `host not allowed`: o cliente usou um nome de host que não é loopback
  nem está em `AI_OBSERVER_ALLOWED_HOSTS`. Use `http://127.0.0.1:<porta>`.

## `400 not a forward proxy`

Algo usou o endereço como `HTTP_PROXY`/`HTTPS_PROXY`. O inspector é um
**reverse proxy**: configure `ANTHROPIC_BASE_URL`, não `HTTPS_PROXY`.

## `502` / `504` com "AI Observer HTTP proxy: upstream … unreachable"

O proxy não alcançou o upstream (rede, DNS, firewall, proxy corporativo).
O exchange fica `failed`. Confira `AI_OBSERVER_HTTP_PROXY_UPSTREAM` e a
conectividade com `api.anthropic.com`.

## Nada aparece no inspector

1. `ANTHROPIC_BASE_URL` foi ignorado? No processo do Claude Code:
   `echo $ANTHROPIC_BASE_URL`. Configurações em `~/.claude/settings.json`
   (`env`) ou variáveis de gerenciamento podem sobrepor o ambiente. O
   launcher define a variável só no processo filho.
2. O proxy está ligado? `/api/http-inspector/status` → `"enabled": true`.
3. A sessão não aparece em Sessões? A lista de sessões vem da telemetria
   (OTLP/JSONL). Uma sessão vista só pelo proxy é acessível pela execução
   em **HTTP Inspector → execuções** e pelos links das trocas.
4. Exchanges sem sessão: o cliente não enviou `X-Claude-Code-Session-Id`
   nem `metadata.user_id`. Eles continuam na execução.

## SSE não aparece em tempo real / eventos sem tempo

- O cliente recebe o stream sem buffer (`FlushInterval -1`). Se a interface
  só atualiza no fim, verifique a conexão WebSocket do dashboard (`/ws`).
- Respostas SSE com `Content-Encoding` são interpretadas ao final: os
  eventos aparecem sem tempo individual (nota no lifecycle).

## Tool search / `defer_loading`

O Claude Code recente envia ferramentas com `defer_loading: true` e blocos
`tool_reference` mesmo através de `ANTHROPIC_BASE_URL`. O proxy repassa os
bytes sem alteração (teste AC-17) e o inspector conta as ferramentas
adiadas. Se o seu gateway ou versão se comportar diferente, compare o
request no inspector com a documentação do Claude Code sobre tool search
atrás de gateway.

## Assinatura (claude.ai) e credenciais

O launcher não toca no login. O uso com assinatura via `ANTHROPIC_BASE_URL`
não foi verificado com tráfego real (ver [SECURITY.md](SECURITY.md)). O
custo mostrado é sempre **equivalente de API**, não a cobrança da
assinatura.

## Contagens diferentes entre proxy, OTLP e JSONL

São fontes diferentes e **não são somadas**:

- `count_tokens` é estimativa e fica separado da inferência;
- `output_tokens` do proxy é o último valor cumulativo do stream;
- OTLP pode faltar (exportador desligado, intervalo de exportação) e o
  JSONL registra mensagens, não requests HTTP; retries e chamadas
  auxiliares (títulos, verificações) aparecem só no proxy;
- a seção de comparação da aba "Tráfego da API" mostra quantas requests
  casaram pelo `request_id` e a certeza.

## Docker / host

O proxy escuta em `127.0.0.1` por padrão; dentro de um container isso não
é alcançável de fora. Para expor, defina
`AI_OBSERVER_HTTP_PROXY_HOST=0.0.0.0` **e**
`AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE=true`, publique a porta só em
`127.0.0.1` do host (`-p 127.0.0.1:8787:8787`) e use
`AI_OBSERVER_AUTH_TOKEN` no dashboard. Não exponha o proxy à rede.

## Logs vazios

- `run --serve`: o servidor registra em `ai-observer-run.log` ao lado do
  banco, não no terminal (que pertence ao Claude Code).
- Requests bem-sucedidas são registradas em `DEBUG`
  (`AI_OBSERVER_LOG_LEVEL=DEBUG`).

## Captura marcada como `degraded`

A captura perdeu dados (fila cheia, orçamento de memória, disco), mas o
tráfego foi entregue inteiro. Veja `degraded_reasons` no Resumo do exchange
e os contadores do status. Aumente `AI_OBSERVER_HTTP_CAPTURE_QUEUE_SIZE`,
libere disco ou use `metadata_only`.

## Exchanges `interrupted`

O servidor parou com a execução ativa. No próximo início, execuções e
exchanges em andamento são marcados `interrupted`; o que foi gravado
continua disponível.
