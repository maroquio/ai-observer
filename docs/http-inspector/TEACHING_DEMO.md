# Roteiro de aula: o que o Claude Code realmente manda para a API (10–15 min)

Objetivo: mostrar, com o tráfego no fio, o ciclo
**request → `tool_use` → execução local → `tool_result` → novo request**,
o efeito do cache de prompt e a diferença entre contar tokens e gerar.

Há duas formas de rodar:

- **Simulada (recomendada para aula, determinística, sem custo):** o
  upstream é o `fake-anthropic` deste repositório. O Claude Code é o real;
  o "modelo" é uma simulação que sempre pede para ler `src/auth.ts`.
  **Diga à turma que as respostas do modelo são simuladas.**
- **Real:** mesmo roteiro contra `api.anthropic.com` com a sua conta. Gera
  consumo, e não há garantia de que o modelo use uma ferramenta com um
  prompt específico.

**Alunos com Docker** (`docker compose up -d --build`, [QUICKSTART](QUICKSTART.md)):
modo real, com `export ANTHROPIC_BASE_URL=http://127.0.0.1:8787` antes do
`claude`. O launcher (`ai-observer run`) e o upstream simulado precisam dos
binários nativos: ficam para a máquina do professor. Peça que a imagem seja
compilada antes da aula (alguns minutos e download grande).

## Preparação (antes da aula)

```bash
make backend fake-anthropic
export DEMO=$PWD/docs/http-inspector/demo-project
export PATH=$PWD/bin:$PATH
```

Banco separado para a aula (não mistura com seus dados):

```bash
export AI_OBSERVER_DATABASE_PATH=/tmp/aula-http-inspector/ai-observer.duckdb
```

## 1. Iniciar o AI Observer com o proxy (1 min)

Terminal A (simulado):

```bash
cd "$DEMO"
fake-anthropic --listen 127.0.0.1:9787 --file "$DEMO/src/auth.ts" &
AI_OBSERVER_HTTP_PROXY_UPSTREAM=http://127.0.0.1:9787 ai-observer serve --http-proxy
```

Real: só `ai-observer serve --http-proxy`.

Abra `http://127.0.0.1:8080/http-inspector`: o status mostra proxy ligado,
modo `safe`, upstream.

## 2. Iniciar o Claude Code pelo proxy sem mexer na autenticação (1 min)

Terminal B:

```bash
cd "$DEMO"
# simulado: qualquer chave serve, nada sai da máquina
export ANTHROPIC_API_KEY="chave-de-demonstracao"
ai-observer run --name "Aula — auth.ts" -- claude -p "Revise o arquivo src/auth.ts e aponte um problema de segurança" --allowedTools Read
```

Ponto a explicar: o launcher só troca `ANTHROPIC_BASE_URL` no processo do
Claude Code. O login (chave ou assinatura) passa intacto, e o proxy nunca
grava a credencial — mostre depois o cabeçalho `X-Api-Key: [REDACTED]`.

## 3. Primeiro request: o que estava no fio (3 min)

Abra o link impresso pelo launcher → a execução → exchange **#1** → aba
**Request**:

- `system`: os blocos realmente enviados (tamanho, hash, onde está o
  `cache_control`). Mostre que é muito maior que o prompt digitado.
- `tools`: todas as definições de ferramentas enviadas (Read, Bash, Edit…),
  com seus schemas; algumas com `defer_loading` (tool search).
- `messages`: uma única mensagem do usuário.
- Alterne **Estruturado / JSON original**.

Frase para a turma: "isto é o que o modelo recebe; o resto é o Claude Code".

## 4. A resposta pede uma ferramenta (2 min)

Exchange #1 → **Response**: texto curto + bloco `tool_use` `Read` com
`file_path`, `stop_reason = tool_use`.

Ênfase: o modelo **pediu** a leitura. Quem lê o arquivo é o Claude Code, na
máquina do aluno — o proxy **não vê** essa execução
(nota na lista de ferramentas da execução).

## 5. Os eventos SSE com seus tempos (2 min)

Exchange #1 → **SSE Stream**: `message_start`, `ping`,
`content_block_start`, vários `content_block_delta` (`text_delta`, depois
`input_json_delta` montando o JSON da ferramenta), `content_block_stop`,
`message_delta` (com `stop_reason` e `output_tokens` cumulativo),
`message_stop`. Mostre a coluna de tempo relativo e o filtro por tipo.

## 6. O segundo request leva o resultado (2 min)

Exchange **#2** → **Context Diff**: comparado com o #1 da **mesma
conversa**: prefixo comum de 1 mensagem, +2 mensagens (o `tool_use` do
assistente e o `tool_result` do usuário), `system` e `tools` iguais
(mesmos hashes), breakpoint de cache movido para a mensagem nova.

Ferramentas da execução: `toolu_… Read` → **matched**, com links para o
exchange que pediu (#1) e o que devolveu o resultado (#2).

## 7. Tokens e cache entre as chamadas (2 min)

Aba **Tokens & Cache** dos dois exchanges (valores do upstream simulado):

| | #1 | #2 |
|---|---|---|
| input | 24 | 180 |
| cache write (5m) | 3000 | 420 |
| cache read | 0 | 3000 |
| output | ~20 | ~45 |

(O `output` simulado varia com o texto gerado, que inclui o caminho do
arquivo; input e cache são fixos.)

Leitura: o #1 **escreve** o prefixo longo no cache (paga escrita); o #2
**lê** esse prefixo do cache (muito mais barato) e só paga input pelo que é
novo. O `output_tokens` é cumulativo no stream: o total é o último valor,
não a soma dos eventos. O custo é "equivalente de API" pelo catálogo.

## 8. `count_tokens` não é geração (1 min)

Numa sessão interativa (`ai-observer run -- claude`), o Claude Code pode
chamar `POST /v1/messages/count_tokens`. Ele aparece na timeline como
**contagem de tokens**: tem `input_tokens`, não tem resposta do modelo, e
não entra nos totais de inferência nem no custo. Em modo simulado, mostre
o teste `count_tokens` da suíte ou envie um com `curl`:

```bash
curl -s http://127.0.0.1:8787/v1/messages/count_tokens \
  -H 'content-type: application/json' -H 'x-api-key: chave-de-demonstracao' \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"oi"}]}'
```

(A porta 8787 é a compartilhada: o exchange entra numa execução `manual`.)

## 9. Encerrar, reiniciar e abrir o histórico (1 min)

Pare o servidor (Ctrl+C no terminal A) e inicie de novo com o mesmo banco.
Abra o mesmo link: a execução, os requests, responses, eventos SSE e o
diff continuam lá.

## Perguntas para a turma

1. Quantos bytes de `system` e `tools` foram enviados para responder a uma
   pergunta de uma linha?
2. Por que o segundo request é mais barato em input, apesar de maior?
3. O que o proxy **não** consegue afirmar sobre a ferramenta `Read`?
4. Por que somar `count_tokens` com o uso de inferência estaria errado?

## Limpeza

```bash
rm -rf /tmp/aula-http-inspector
```
