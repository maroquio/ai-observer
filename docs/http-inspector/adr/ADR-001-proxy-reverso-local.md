# ADR-001 — Proxy reverso local via `ANTHROPIC_BASE_URL`

Status: aceito · 2026-11-24

## Contexto

O AI Observer recebe OTLP e lê arquivos de sessão, mas nenhum dos dois traz
os corpos HTTP (system prompt, tools, SSE, `count_tokens`). Para vê-los é
preciso estar no caminho entre o Claude Code e a API.

## Opções

1. MITM de TLS (proxy HTTPS com certificado instalado) — intercepta outros
   apps, exige confiança em CA local, fora de escopo.
2. Forward proxy (`HTTPS_PROXY`) — só vê `CONNECT` cifrado sem MITM.
3. **Reverse proxy** local com `ANTHROPIC_BASE_URL` — o cliente fala HTTP
   com o proxy, o proxy fala HTTPS com o upstream fixo.

## Decisão

Opção 3, em Go (`net/http/httputil.ReverseProxy`), dentro do binário do AI
Observer, desligado por padrão, em `127.0.0.1`. Upstream fixo e validado
(sem SSRF/open proxy); `CONNECT`, URIs absolutas, `Origin` e upgrades são
recusados. Streaming sem buffer (`FlushInterval: -1`), sem negociar
compressão, sem `ResponseHeaderTimeout` (respostas longas são normais).
Erros do proxy respondem no formato de erro da Anthropic.

Duas formas de uso: porta compartilhada (execução `manual`) e
`ai-observer run -- <programa>`, que abre uma porta exclusiva por execução
(`127.0.0.1:0`) e só define `ANTHROPIC_BASE_URL` no processo filho.

## Consequências

- Vê apenas o tráfego que passa por `ANTHROPIC_BASE_URL`.
- Não toca em credenciais nem na configuração do Claude Code.
- Nenhum código do `request-logger` foi copiado (licença não confirmada);
  a implementação é própria.
