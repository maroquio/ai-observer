# ADR-007 — Retenção, exclusão, backup e export das capturas

Status: aceito · 2026-11-24

## Decisão

- Retenção própria (`AI_OBSERVER_HTTP_CAPTURE_RETENTION_DAYS`, padrão igual
  a `AI_OBSERVER_RETENTION_DAYS`) e limite dos corpos
  (`AI_OBSERVER_HTTP_CAPTURE_STORAGE_LIMIT`, 2 GB) no mesmo ticker horário da
  retenção de telemetria; apaga execuções **terminadas**, das mais antigas
  para as mais novas.
- Exclusão por execução (API/UI): as seis tabelas numa transação, depois os
  arquivos; execução ativa → `409`. Diretórios órfãos (banco sem a
  execução) são removidos no início.
- `ai-observer delete`, a retenção de telemetria e `AI_OBSERVER_MAX_DB_SIZE`
  não apagam capturas (fontes independentes; AC-18).
- Export por execução: zip com manifest, execução, ferramentas e exchanges
  com corpos **saneados**; nunca a variante raw nem credenciais.
- `ai-observer backup` copia o banco (inclui as tabelas de captura) mas não
  o diretório de corpos; documentado.
- Tabelas no schema base, sem migração: o banco continua compatível com o
  AI Observer original.
