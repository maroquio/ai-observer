# ADR-003 — Backpressure: a captura nunca atrasa o tráfego

Status: aceito · 2026-11-24

## Contexto

Um proxy no caminho do Claude Code não pode deixar a sessão mais lenta nem
falhar por causa de disco, banco ou parser.

## Decisão

- O proxy escreve para o cliente **antes** de entregar o pedaço à captura.
- A entrega é não bloqueante (`select` com `default`) para um canal por
  exchange (512 pedaços) sob um orçamento global de memória (256 MB). Sem
  espaço, o pedaço é descartado, a parte é marcada perdida e o exchange
  `degraded`; o SHA-256 observado deixa de ser publicado.
- Um worker por exchange faz o parse SSE incremental com tempos de chegada.
- Um único goroutine grava no DuckDB em lotes de até 64 registros; a fila
  (`AI_OBSERVER_HTTP_CAPTURE_QUEUE_SIZE`, 1024) descarta quando cheia e conta
  em `droppedRecords`.
- Falhas de gravação viram `storeErrors`/`degraded`. Pânicos no parse são
  recuperados e geram um registro mínimo (fallback).
- Encerramento: listeners param, workers drenam, a fila é gravada antes de
  o storage fechar.

## Consequências

Em sobrecarga extrema, a captura fica incompleta (e diz isso), mas o
cliente recebe tudo. Testes: barreira de streaming, falha de store, falha
de diretório, cancelamento do cliente e reset do upstream no meio do
stream, clientes paralelos, `-race`.
