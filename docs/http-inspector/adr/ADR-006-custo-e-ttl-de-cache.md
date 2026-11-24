# ADR-006 — Custo com o catálogo existente e TTL de cache

Status: aceito · 2026-11-24

## Decisão

- Preços do catálogo do AI Observer (`internal/pricing`, dados de
  models.dev), resolvendo aliases como o resto do sistema e o tier de
  contexto longo pelo tamanho do prompt (`ClaudePricingForPrompt`).
- Componentes: input, output, cache read, cache write 5m e cache write sem
  divisão, cada um com tokens, preço e valor.
- A escrita de cache de **1 h** não tem preço próprio no catálogo: quando
  há tokens 1h, o custo fica `partial` com o motivo — não se usa o preço de
  5m nem se inventa um multiplicador.
- Modelo sem preço → `unavailable` (nunca `$0`, AC-14).
- `count_tokens` nunca é precificado.
- Rótulo sempre "custo equivalente de API": em assinatura claude.ai não é
  o valor cobrado.
- `output_tokens` em streaming é cumulativo: usa-se o último valor
  (AC-03); totais: `input + cache_read + cache_write` e `+ output` (AC-04).

## Consequências

Quando o catálogo ganhar o preço de 1 h, basta acrescentar o componente.
