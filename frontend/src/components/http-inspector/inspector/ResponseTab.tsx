import { useState } from 'react'
import { AlertCircle } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { formatCount } from '@/lib/format'
import { useHTTPExchangeBody } from '@/lib/queries'
import type { WireContentBlock, WireMessage } from '@/types/httpInspector'
import { Absent, Expandable, Fields, JsonBlock, Section, TextBlock } from '../primitives'
import { BodyFrame, type BodyMode } from './BodyFrame'

/** Response: the reply (parsed, or reconstructed from its SSE events) and the body as stored */
export function ResponseTab({ exchangeId }: { exchangeId: string }) {
  const [mode, setMode] = useState<BodyMode>('structured')
  const [variant, setVariant] = useState<'safe' | 'raw'>('safe')
  const query = useHTTPExchangeBody(exchangeId, 'response', variant)
  return (
    <BodyFrame
      partLabel="resposta"
      query={query}
      mode={mode}
      onModeChange={setMode}
      variant={variant}
      onVariantChange={setVariant}
      structured={(body) => (body.message ? <StructuredResponse message={body.message} /> : null)}
    />
  )
}

function StructuredResponse({ message }: { message: WireMessage }) {
  const content = message.content ?? []
  return (
    <div className="space-y-6">
      {message.error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>Erro da API: {message.error.type}</AlertTitle>
          <AlertDescription>{message.error.message}</AlertDescription>
        </Alert>
      )}

      <Fields
        label="Dados da resposta"
        items={[
          { label: 'Modelo', value: message.model || <Absent>—</Absent> },
          { label: 'stop_reason', value: message.stopReason || <Absent>—</Absent> },
          ...(message.stopSequence ? [{ label: 'stop_sequence', value: <span className="font-mono">{message.stopSequence}</span> }] : []),
          { label: 'id', value: message.id ? <span className="font-mono text-xs">{message.id}</span> : <Absent>—</Absent> },
          {
            label: 'Origem',
            value: message.streamed ? 'Reconstruída dos eventos SSE' : 'JSON da resposta',
            hint: message.streamed
              ? `message_start ${message.sawMessageStart ? 'visto' : 'ausente'} · message_stop ${message.sawMessageStop ? 'visto' : 'ausente'}`
              : undefined,
          },
          {
            label: 'Completa',
            value: message.complete ? (
              <Badge variant="success">sim</Badge>
            ) : (
              <Badge variant="warning" title="O stream terminou sem message_stop, ou algum bloco não foi fechado">
                não (parcial)
              </Badge>
            ),
          },
        ]}
      />

      {(message.notes ?? []).length > 0 && (
        <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground" aria-label="Problemas de interpretação">
          {(message.notes ?? []).map((note, i) => (
            <li key={i}>{note}</li>
          ))}
        </ul>
      )}

      <Section title={`Conteúdo (${formatCount(content.length)} ${content.length === 1 ? 'bloco' : 'blocos'})`}>
        {content.length === 0 ? (
          <p className="text-sm text-muted-foreground">A resposta não trouxe blocos de conteúdo.</p>
        ) : (
          <ol className="space-y-4">
            {content.map((block) => (
              <li key={block.index}>
                <ResponseBlock block={block} />
              </li>
            ))}
          </ol>
        )}
      </Section>

      {message.eventCounts && Object.keys(message.eventCounts).length > 0 && (
        <Section title="Eventos do stream">
          <ul className="flex flex-wrap gap-2">
            {Object.entries(message.eventCounts).map(([type, n]) => (
              <li key={type}>
                <Badge variant="outline" className="font-mono">
                  {type} × {formatCount(n)}
                </Badge>
              </li>
            ))}
          </ul>
        </Section>
      )}

      <Expandable title="JSON reconstruído da resposta">
        <JsonBlock value={message} label="Resposta reconstruída" />
      </Expandable>
    </div>
  )
}

function ResponseBlock({ block }: { block: WireContentBlock }) {
  const label = `Bloco ${block.index}`
  const header = (
    <div className="mb-1 flex flex-wrap items-center gap-2 text-xs">
      <Badge variant="outline" className="font-mono">
        [{block.index}] {block.type}
      </Badge>
      {block.name && <span className="font-mono">{block.name}</span>}
      {block.id && <span className="font-mono text-muted-foreground">{block.id}</span>}
      {!block.stopped && (
        <Badge variant="warning" title="content_block_stop não foi recebido">
          bloco não finalizado
        </Badge>
      )}
    </div>
  )

  let body
  switch (block.type) {
    case 'text':
      body = <TextBlock text={block.text ?? ''} label={label} />
      break
    case 'thinking':
      // Shown only as transmitted: the API may send thinking empty or summarized
      body = block.thinking ? (
        <TextBlock text={block.thinking} label={`${label} (thinking transmitido)`} />
      ) : (
        <p className="text-sm text-muted-foreground">Bloco de thinking sem texto transmitido.</p>
      )
      break
    case 'redacted_thinking':
      body = <p className="text-sm text-muted-foreground">Raciocínio opaco (redacted_thinking): conteúdo cifrado, não interpretável.</p>
      break
    case 'tool_use':
    case 'server_tool_use':
    case 'mcp_tool_use':
      body =
        block.input !== undefined ? (
          <JsonBlock value={block.input} label={`${label} (input)`} />
        ) : (
          <div className="space-y-1">
            {block.inputError && <p className="text-sm text-warning">Entrada incompleta: {block.inputError}</p>}
            <TextBlock text={block.partialInput ?? ''} label={`${label} (input parcial)`} />
          </div>
        )
      break
    default:
      body = <JsonBlock value={block.raw ?? block} label={label} />
  }
  return (
    <div>
      {header}
      {body}
    </div>
  )
}
