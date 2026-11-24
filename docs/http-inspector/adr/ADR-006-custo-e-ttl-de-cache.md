# ADR-006 — Custo com o catálogo existente e TTL de cache

Status: aceito · 2026-11-24 · revisado no mesmo dia (preço de 1 h, abaixo)

## Decisão

- Preços do catálogo do AI Observer (`internal/pricing`, dados de
  models.dev), resolvendo aliases como o resto do sistema e o tier de
  contexto longo pelo tamanho do prompt (`ClaudePricingForPrompt`).
- Componentes: input, output, cache read, cache write 5m, cache write 1h e
  cache write sem divisão, cada um com tokens, preço e valor.
- A escrita de cache de **1 h** não tem preço no catálogo (models.dev só
  publica `cache_write`, o de 5 min). Seu preço é **derivado**: 2 × o preço
  de input do catálogo para o mesmo modelo e faixa de contexto, a regra de
  preços da Anthropic (5 min = 1,25 × input, 1 h = 2 × input). O componente
  traz a nota "derivado", e o custo fica `complete`.
- Modelo sem preço → `unavailable` (nunca `$0`, AC-14).
- `count_tokens` nunca é precificado.
- Rótulo sempre "custo equivalente de API": em assinatura claude.ai não é
  o valor cobrado.
- `output_tokens` em streaming é cumulativo: usa-se o último valor
  (AC-03); totais: `input + cache_read + cache_write` e `+ output` (AC-04).

## Revisão: preço derivado para 1 h

A primeira versão deixava o custo `partial` sempre que havia escrita de
1 h, sem multiplicador. O smoke real com login por assinatura
([TESTING.md](../TESTING.md); uma conta, Haiku, `claude -p`) teve **toda**
a escrita de cache com TTL de 1 h: o custo mostrado omitia cerca de 94% do
valor. O `costUSD` que o próprio Claude Code envia na telemetria é
exatamente `input + output + cache_read + cache_write_1h × 2 × preço_input`
(na primeira execução pela conta sobre os tokens capturados; na segunda,
com o binário corrigido, pelo custo que o proxy calculou). Por decisão do usuário, o proxy passou a usar a mesma regra.
Não é um preço inventado por modelo: é a razão fixa publicada, aplicada ao
preço de input do catálogo.

## Consequências

Quando o catálogo ganhar o preço de 1 h, ele substitui o derivado. Se a
Anthropic mudar a razão, muda a constante `cacheWrite1hInputMultiplier`
(`internal/httpcapture/finalize.go`).
