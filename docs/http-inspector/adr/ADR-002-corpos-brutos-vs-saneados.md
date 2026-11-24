# ADR-002 — Corpos: saneado por padrão, bruto só por opt-in, metadados opcionais

Status: aceito · 2026-11-24

## Contexto

Os corpos contêm prompts, código e, às vezes, segredos colados pelo
usuário. Ao mesmo tempo, a aula precisa do conteúdo real.

## Decisão

- Três modos (`AI_OBSERVER_HTTP_CAPTURE_MODE`): `safe` (padrão),
  `metadata_only`, `full_raw`.
- Headers de credencial **nunca** são gravados, em nenhum modo: são
  mascarados antes do enfileiramento.
- `safe`: corpo decodificado (gzip/deflate/br/zstd) e saneado no nível dos
  valores JSON (chaves sensíveis trocadas inteiras + redator do AI Observer),
  preservando ordem e validade do JSON; conta as substituições.
- `full_raw`: grava também a variante `raw` (decodificada, sem redação),
  com aviso no log; a API só a entrega com `?variant=raw`; exports nunca a
  incluem.
- `metadata_only`: nenhum corpo, eventos sem `data`, resumos sem trechos.
- Corpos ficam em **arquivos** (não no banco), referenciados por
  `http_capture_payloads` com SHA-256 observado e gravado.
- Corpos acima de `MAX_BODY_BYTES` (32 MB) são gravados cortados; o
  tráfego segue inteiro.

## Consequências

O banco continua pequeno e consultável; apagar uma execução remove linhas
e arquivos. O SHA-256 observado permite provar que o que foi gravado
corresponde ao que passou (ou que houve perda).
