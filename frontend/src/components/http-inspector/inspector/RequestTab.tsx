import { useMemo, useState, type ReactNode } from 'react'
import { Badge } from '@/components/ui/badge'
import { formatBytes, formatCount } from '@/lib/format'
import { isJsonObject, parseJSON } from '@/lib/httpInspector'
import { useHTTPExchangeBody } from '@/lib/queries'
import type { HTTPExchangeBody, WireRequestSummary } from '@/types/httpInspector'
import { Absent, Expandable, Fields, JsonBlock, Section } from '../primitives'
import { BodyFrame, type BodyMode } from './BodyFrame'
import { CacheControlBadge, ContentBlocks } from './ContentBlocks'

// Messages shown open at first: the last ones, which the request added
const OPEN_LAST_MESSAGES = 2

/** Request: the parsed Messages API request and the body as stored */
export function RequestTab({ exchangeId }: { exchangeId: string }) {
  const [mode, setMode] = useState<BodyMode>('structured')
  const [variant, setVariant] = useState<'safe' | 'raw'>('safe')
  const query = useHTTPExchangeBody(exchangeId, 'request', variant)
  return (
    <BodyFrame
      partLabel="requisição"
      query={query}
      mode={mode}
      onModeChange={setMode}
      variant={variant}
      onVariantChange={setVariant}
      structured={(body) => (body.request ? <StructuredRequest summary={body.request} body={body} /> : null)}
    />
  )
}

function StructuredRequest({ summary, body }: { summary: WireRequestSummary; body: HTTPExchangeBody }) {
  // The stored body (safe or raw) adds the text the summary only measures;
  // without one (metadata_only) the summary's sizes and hashes remain
  const json = useMemo(() => {
    if (body.bodyEncoding === 'base64') return undefined
    const parsed = parseJSON(body.body)
    return isJsonObject(parsed) ? parsed : undefined
  }, [body.body, body.bodyEncoding])

  if (!summary.valid) {
    return <p className="text-sm text-error">Corpo não interpretado como requisição da Messages API: {summary.parseError ?? 'JSON inválido'}</p>
  }

  const params: Array<{ label: string; value: ReactNode }> = [
    { label: 'model', value: summary.model ?? <Absent /> },
    { label: 'max_tokens', value: summary.maxTokens != null ? formatCount(summary.maxTokens) : <Absent /> },
    { label: 'stream', value: summary.stream != null ? String(summary.stream) : <Absent /> },
    { label: 'temperature', value: summary.temperature != null ? String(summary.temperature) : <Absent /> },
    { label: 'top_p', value: summary.topP != null ? String(summary.topP) : <Absent /> },
    { label: 'top_k', value: summary.topK != null ? String(summary.topK) : <Absent /> },
  ]
  const jsonParams: Array<[string, unknown]> = [
    ['thinking', summary.thinking],
    ['tool_choice', summary.toolChoice],
    ['output_config', summary.outputConfig],
    ['context_management', summary.contextManagement],
    ['stop_sequences', summary.stopSequences],
    ['metadata', summary.metadata],
  ]
  const toolDefs = Array.isArray(json?.tools) ? json.tools : []
  const messages = Array.isArray(json?.messages) ? json.messages : undefined
  const messageCount = summary.messages.count

  return (
    <div className="space-y-6">
      <Section title="Parâmetros">
        <Fields items={params.map((p) => ({ label: <span className="font-mono">{p.label}</span>, value: p.value, key: p.label }))} />
        <div className="grid gap-2 lg:grid-cols-2">
          {jsonParams
            .filter(([, value]) => value !== undefined && value !== null)
            .map(([name, value]) => (
              <Expandable key={name} title={<span className="font-mono">{name}</span>}>
                <JsonBlock value={value} label={name} />
              </Expandable>
            ))}
        </div>
        {(summary.unknownFields ?? []).length > 0 && (
          <p className="text-xs text-muted-foreground">
            Campos não reconhecidos: <span className="font-mono">{(summary.unknownFields ?? []).join(', ')}</span>
          </p>
        )}
      </Section>

      <Section
        title="System"
        actions={
          summary.system.present ? (
            <span className="text-xs text-muted-foreground">
              {summary.system.kind === 'blocks' ? `${formatCount(summary.system.blocks)} blocos` : 'texto'} · {formatBytes(summary.system.bytes)}
              {summary.system.sha256 && <span className="ml-1 font-mono">sha256 {summary.system.sha256.slice(0, 12)}…</span>}
            </span>
          ) : undefined
        }
      >
        {!summary.system.present ? (
          <p className="text-sm text-muted-foreground">A requisição não tem system prompt.</p>
        ) : json?.system !== undefined ? (
          <ContentBlocks content={json.system} label="System" />
        ) : (
          <p className="text-sm text-muted-foreground">Texto não armazenado (somente tamanho e hash).</p>
        )}
      </Section>

      <Section
        title={`Ferramentas (${formatCount(summary.tools.count)})`}
        actions={
          summary.tools.count > 0 ? (
            <span className="text-xs text-muted-foreground">
              {formatBytes(summary.tools.bytes)}
              {summary.tools.deferred > 0 && ` · ${formatCount(summary.tools.deferred)} com defer_loading`}
            </span>
          ) : undefined
        }
      >
        {summary.tools.count === 0 ? (
          <p className="text-sm text-muted-foreground">Nenhuma ferramenta definida.</p>
        ) : (
          <ul className="space-y-1">
            {(summary.tools.tools ?? []).map((tool, i) => (
              <li key={`${tool.name}:${i}`}>
                <Expandable
                  title={
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="font-mono">{tool.name}</span>
                      {tool.type && <Badge variant="outline" className="font-mono text-[10px]">{tool.type}</Badge>}
                      {tool.deferLoading && <Badge variant="secondary" className="text-[10px]">defer_loading</Badge>}
                      <CacheControlBadge value={isJsonObject(toolDefs[i]) ? toolDefs[i].cache_control : undefined} />
                      <span className="text-xs text-muted-foreground">{formatBytes(tool.bytes)}</span>
                    </span>
                  }
                >
                  {toolDefs[i] !== undefined ? (
                    <JsonBlock value={toolDefs[i]} label={`Definição de ${tool.name}`} />
                  ) : (
                    <p className="text-sm text-muted-foreground">
                      Definição não armazenada · sha256 <span className="font-mono">{tool.sha256.slice(0, 16)}</span>
                    </p>
                  )}
                </Expandable>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section
        title={`Mensagens (${formatCount(messageCount)})`}
        actions={
          <span className="text-xs text-muted-foreground">
            {Object.entries(summary.messages.roles ?? {})
              .map(([role, n]) => `${role}: ${formatCount(n)}`)
              .join(' · ')}
            {' · '}
            {formatBytes(summary.messages.bytes)}
          </span>
        }
      >
        {Object.keys(summary.messages.blocks ?? {}).length > 0 && (
          <p className="text-xs text-muted-foreground">
            Blocos por tipo:{' '}
            <span className="font-mono">
              {Object.entries(summary.messages.blocks ?? {})
                .map(([type, n]) => `${type} ${formatCount(n)}`)
                .join(', ')}
            </span>
          </p>
        )}
        <ol className="space-y-1" aria-label="Mensagens da requisição">
          {Array.from({ length: messages?.length ?? messageCount }, (_, i) => {
            const message = messages?.[i]
            const role = isJsonObject(message) && typeof message.role === 'string' ? message.role : undefined
            const size = summary.messages.sizes?.[i]
            const hash = summary.messages.hashes?.[i]
            return (
              <li key={i}>
                <Expandable
                  defaultOpen={i >= (messages?.length ?? messageCount) - OPEN_LAST_MESSAGES}
                  title={
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-xs text-muted-foreground">messages[{i}]</span>
                      {role && <Badge variant={role === 'user' ? 'secondary' : 'outline'}>{role}</Badge>}
                      {size !== undefined && <span className="text-xs text-muted-foreground">{formatBytes(size)}</span>}
                      {hash && <span className="font-mono text-[10px] text-muted-foreground">{hash}</span>}
                    </span>
                  }
                >
                  {isJsonObject(message) ? (
                    <ContentBlocks content={message.content} label={`Mensagem ${i}`} />
                  ) : (
                    <p className="text-sm text-muted-foreground">Conteúdo não armazenado (somente tamanho e hash).</p>
                  )}
                </Expandable>
              </li>
            )
          })}
        </ol>
      </Section>

      {(summary.toolResults ?? []).length > 0 && (
        <Section title="tool_result nesta requisição">
          <ul className="space-y-2">
            {(summary.toolResults ?? []).map((ref) => (
              <li key={`${ref.toolUseId}:${ref.messageIndex}:${ref.blockIndex}`} className="space-y-1 rounded-md border p-3 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs">{ref.toolUseId}</span>
                  <span className="text-xs text-muted-foreground">
                    messages[{ref.messageIndex}].content[{ref.blockIndex}] · {formatBytes(ref.contentBytes)}
                  </span>
                  {ref.isError && <Badge variant="destructive">is_error</Badge>}
                </div>
                {ref.preview && <p className="font-mono text-xs break-words whitespace-pre-wrap text-muted-foreground">{ref.preview}</p>}
              </li>
            ))}
          </ul>
        </Section>
      )}

      <Section title="Pontos de cache (cache_control)">
        {(summary.cacheBreakpoints ?? []).length === 0 ? (
          <p className="text-sm text-muted-foreground">Nenhum cache_control na requisição.</p>
        ) : (
          <ul className="space-y-1 text-sm">
            {(summary.cacheBreakpoints ?? []).map((bp, i) => (
              <li key={`${bp.location}:${i}`} className="font-mono text-xs">
                {bp.location} {bp.type && `type=${bp.type}`} {bp.ttl ? `ttl=${bp.ttl}` : 'ttl padrão (5m)'}
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Expandable title="Resumo interpretado (JSON)">
        <JsonBlock value={summary} label="Resumo da requisição" />
      </Expandable>
    </div>
  )
}
