# HTTP Inspector — segurança e privacidade

O HTTP Inspector fica no caminho entre o Claude Code e a API: vê
credenciais, prompts, código-fonte, saídas de ferramentas e respostas. Por
isso ele é **desligado por padrão**, **local** e grava em **modo seguro**.

## Modelo de ameaças

| Ameaça | Controle |
|---|---|
| Vazamento da chave/token do usuário | Headers de credencial (`Authorization`, `Proxy-Authorization`, `x-api-key`, `api-key`, `Cookie`, `Set-Cookie`, `anthropic-admin-api-key` e nomes com `token`, `secret`, `password`, `api-key`, `-auth`) são substituídos por `[REDACTED]` (mantendo só o esquema `Bearer`) **antes** de qualquer gravação, em todos os modos, inclusive `full_raw`. Testado: banco, arquivos de corpo e export não contêm a chave (AC-12) |
| Segredos dentro dos corpos | Modo `safe` (padrão): valores de chaves sensíveis (`password`, `token`, `api_key`, `secret`, `private_key`…) são trocados inteiros; o restante passa pelo redator do AI Observer (`AI_OBSERVER_REDACT`: formatos `sk-ant-`, `ghp_`, `AKIA`, JWT, PEM, URLs com credencial…). A interface mostra que houve mascaramento e quantas trocas (AC-13) |
| Proxy aberto / SSRF | Upstream **fixo**, validado no início: `https://` com host, sem caminho, query, fragmento ou credenciais; `http://` só para loopback (testes). O destino nunca vem da request. `CONNECT` e URIs absolutas (uso como forward proxy) são recusados (`400`) |
| Acesso pela rede | Escuta em `127.0.0.1` por padrão; outra interface exige `AI_OBSERVER_HTTP_CAPTURE_ALLOW_REMOTE=true`. `Host` diferente de loopback/hosts permitidos → `403` |
| CSRF / DNS rebinding a partir do navegador | Requests com cabeçalho `Origin` (páginas web) são recusadas (`403`); o Claude Code não envia `Origin`. Upgrades (WebSocket) → `501` |
| Leitura das capturas por terceiros | A API do dashboard já tem Host/Origin check e, com `AI_OBSERVER_AUTH_TOKEN`, exige token; se a API escuta além de loopback sem token, o servidor avisa no log que o inspector mostra prompts e respostas |
| XSS com conteúdo capturado | A interface renderiza payloads somente como texto/JSON (sem `dangerouslySetInnerHTML`, sem Markdown, sem `href` vindo do payload); teste com `<img onerror>`, `<script>` e `javascript:` (AC-15) |
| Path traversal nos arquivos | Corpos gravados via `os.Root` no diretório de captura; IDs validados; nenhum caminho vem da request |
| Disco/banco cheio ou lento afetando o uso | Captura não bloqueante com orçamento de memória e filas limitadas; falha vira `degraded`/`storeErrors`, o tráfego continua (AC-11) |
| Capturas acumulando indefinidamente | Retenção (`AI_OBSERVER_HTTP_CAPTURE_RETENTION_DAYS`) e limite de armazenamento (`AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT`, 2 GB) |

## Modos de captura

| Modo | Corpos | Eventos SSE | Uso/custo/resumos |
|---|---|---|---|
| `safe` (padrão) | variante `safe`: decodificada e saneada | JSON saneado | sim |
| `metadata_only` | nenhum | só tipo, tempo e tamanho | sim (sem trechos de conteúdo) |
| `full_raw` | `safe` **e** `raw` (decodificada, sem redação) | JSON saneado | sim |

`full_raw` é opt-in, registra um aviso no início e a variante raw só é lida
pela interface/API quando pedida (`?variant=raw`). Credenciais de header
continuam mascaradas mesmo em `full_raw`. O export de execuções **nunca**
inclui a variante raw.

## Armazenamento local

- Banco DuckDB do AI Observer (tabelas `http_capture_*`).
- Corpos em `AI_OBSERVER_HTTP_CAPTURE_DIR` (padrão `http-captures` ao lado do
  banco), diretórios `0700`, arquivos `0600`.
- Nada é enviado a serviços externos; o proxy só fala com o upstream fixo.
- Execuções podem ser apagadas individualmente (banco e arquivos).

## Export

`GET /api/http-inspector/runs/{id}/export` gera um zip local com a execução,
os exchanges, os vínculos de ferramentas e os corpos **saneados**. Mesmo
saneado, o export contém prompts e código: trate-o como dado sensível antes
de compartilhar. Screenshots e exemplos da documentação usam apenas dados
fictícios.

## O que o proxy **não** vê

- A **execução local** das ferramentas: ele vê o `tool_use` pedido pelo
  modelo e o `tool_result` que o Claude Code envia depois — não o que
  aconteceu entre os dois.
- Chamadas que não passam por `ANTHROPIC_BASE_URL` (outros processos,
  outras ferramentas, telemetria do cliente, chamadas a outros hosts).
- `thinking` que a API não transmitiu; nada é inferido.
- Cobrança real: o custo mostrado é **equivalente de API** pelo catálogo de
  preços; numa assinatura claude.ai não corresponde ao que é cobrado.
- Tráfego TLS de outros aplicativos: não há MITM nem certificados
  instalados; o Claude Code fala HTTP com o proxy local porque
  `ANTHROPIC_BASE_URL` aponta para ele, e o proxy fala HTTPS com a API.

## Limitações conhecidas

- Login por assinatura (OAuth claude.ai) através de `ANTHROPIC_BASE_URL`
  **não foi verificado** com tráfego real (exigiria uma conta e geraria
  consumo). Com API key, o fluxo foi verificado com chave fictícia contra o
  upstream simulado.
- Respostas SSE com `Content-Encoding` (gzip etc.) são repassadas sem
  atraso, mas interpretadas só ao final: os eventos aparecem sem tempo de
  chegada individual. A API normalmente não comprime SSE.
- O redator é baseado em padrões: um segredo sem formato reconhecível e
  fora de uma chave sensível pode passar. Para material especialmente
  sensível use `metadata_only`.
