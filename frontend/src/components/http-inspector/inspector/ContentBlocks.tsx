import { Badge } from '@/components/ui/badge'
import { formatCount } from '@/lib/format'
import { isJsonObject as isObject, jsonText } from '@/lib/httpInspector'
import { JsonBlock, TextBlock } from '../primitives'

// Content of Messages API requests (system and message content blocks), as
// parsed from a stored body. Everything is untrusted and shown as text:
// images and documents are described, never loaded.

function str(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

/** A cache_control marker of a block */
export function CacheControlBadge({ value }: { value: unknown }) {
  if (!isObject(value)) return null
  const ttl = str(value.ttl)
  return (
    <Badge variant="secondary" className="font-mono text-[10px]" title="Ponto de cache (cache_control): o prefixo até aqui pode ser lido do cache">
      cache_control{ttl ? ` ttl=${ttl}` : ''}
    </Badge>
  )
}

/** A message's or system's content: a string, or a list of blocks */
export function ContentBlocks({ content, label }: { content: unknown; label: string }) {
  if (typeof content === 'string') {
    return <TextBlock text={content} label={label} />
  }
  if (!Array.isArray(content)) {
    return <JsonBlock value={content} label={label} />
  }
  if (content.length === 0) {
    return <p className="text-sm text-muted-foreground">Sem conteúdo.</p>
  }
  return (
    <ol className="space-y-3">
      {content.map((block, i) => (
        <li key={i}>
          <ContentBlock block={block} label={`${label}, bloco ${i}`} index={i} />
        </li>
      ))}
    </ol>
  )
}

function ContentBlock({ block, label, index }: { block: unknown; label: string; index: number }) {
  if (!isObject(block)) {
    return <JsonBlock value={block} label={label} />
  }
  const type = str(block.type) ?? 'desconhecido'
  const header = (
    <div className="mb-1 flex flex-wrap items-center gap-2 text-xs">
      <Badge variant="outline" className="font-mono">
        [{index}] {type}
      </Badge>
      <CacheControlBadge value={block.cache_control} />
      {type === 'tool_use' && (
        <span className="font-mono">
          {str(block.name)} · {str(block.id)}
        </span>
      )}
      {type === 'tool_result' && (
        <span className="font-mono">
          tool_use_id={str(block.tool_use_id)}
          {block.is_error === true && (
            <Badge variant="destructive" className="ml-2">
              is_error
            </Badge>
          )}
        </span>
      )}
    </div>
  )

  let body
  switch (type) {
    case 'text':
      body = <TextBlock text={str(block.text) ?? ''} label={label} />
      break
    case 'thinking':
      body = <TextBlock text={str(block.thinking) ?? ''} label={`${label} (thinking)`} />
      break
    case 'redacted_thinking':
      body = <p className="text-sm text-muted-foreground">Raciocínio opaco (redacted_thinking): conteúdo cifrado, não interpretável.</p>
      break
    case 'tool_use':
      body = <JsonBlock value={block.input} label={`${label} (input)`} />
      break
    case 'tool_result':
      body = <ContentBlocks content={block.content ?? ''} label={`${label} (resultado)`} />
      break
    case 'image':
    case 'document': {
      const source = isObject(block.source) ? block.source : {}
      const data = str(source.data)
      body = (
        <p className="text-sm text-muted-foreground">
          {type === 'image' ? 'Imagem' : 'Documento'} ({str(source.type) ?? 'fonte desconhecida'}
          {str(source.media_type) ? `, ${str(source.media_type)}` : ''}
          {data ? `, ${formatCount(data.length)} caracteres de dados` : ''}) — não exibido.
        </p>
      )
      break
    }
    default:
      body = <TextBlock text={jsonText(block)} label={label} />
  }
  return (
    <div>
      {header}
      {body}
    </div>
  )
}
