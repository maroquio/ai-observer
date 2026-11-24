# ADR-005 — Sem dupla contagem entre OTLP, JSONL e proxy

Status: aceito · 2026-11-24

## Contexto

A mesma request do Claude Code pode ser vista três vezes: evento OTLP
`api_request`, mensagem no JSONL e exchange no proxy. O AI Observer já
reconcilia OTLP × JSONL (`storage/usage_overlap.go`).

## Decisão

- O proxy grava em tabelas próprias (`http_capture_*`), nunca em
  `otel_logs`/`otel_metrics`. Logo, `/api/stats`, séries de métricas,
  `usage_summary` e totais de sessões **não mudam** com o proxy ligado
  (testado: contagens iguais antes/depois — AC-08, AC-16).
- A sessão mostra os números do proxy **ao lado** da telemetria, com uma
  comparação: requests do proxy casadas com `api_request` pelo `request_id`
  (cabeçalho `request-id` da resposta = atributo `request_id` do OTLP),
  certeza `exact`/`partial`/`none` e notas. Nada é somado.
- `count_tokens` fica separado (`count_tokens`), fora de input/output e custo.

## Consequências

Os totais oficiais continuam vindo da telemetria existente; o proxy é a
fonte de detalhe e de auditoria. Uma sessão só capturada pelo proxy não
entra nos totais do dashboard (decisão consciente: evitar uma terceira regra
de reconciliação sem evidência suficiente).
