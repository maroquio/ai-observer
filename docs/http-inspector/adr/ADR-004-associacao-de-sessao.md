# ADR-004 — Associação do exchange à sessão do Claude Code

Status: aceito · 2026-11-24

## Contexto

A aba "Tráfego da API" precisa aparecer na sessão que o AI Observer já
conhece (pelo OTLP `session.id` ou pelos arquivos JSONL).

## Evidência verificada

No binário do Claude Code **2.1.331**:

- toda request leva `X-Claude-Code-Session-Id: <id da sessão>`;
- `metadata.user_id` é a string JSON
  `{"device_id":…,"account_uuid":…,"session_id":<mesmo id>,…}`;
  versões antigas usavam `user_<hex>_account_<uuid>_session_<uuid>`.

## Decisão

Ordem: cabeçalho → `metadata.user_id` (JSON ou formato antigo). O ID é
validado (`[A-Za-z0-9._:-]{1,128}`) e a **evidência** é gravada
(`header:X-Claude-Code-Session-Id` / `body:metadata.user_id`) e mostrada na
interface. Sem evidência, o exchange fica sem sessão, visível na execução.
O launcher acrescenta outra garantia: a porta é exclusiva da execução, então
exchanges de execuções paralelas nunca se misturam (AC-07).

## Consequências

Nenhuma heurística por horário. Se uma versão futura do Claude Code deixar
de enviar ambos, os exchanges continuam acessíveis pela execução.
