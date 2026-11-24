import { useState, type ReactNode } from 'react'
import { ChevronRight, ShieldAlert } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { CopyButton } from '@/components/common/copy-button'
import { formatCount } from '@/lib/format'
import {
  KIND_LABELS,
  ORIGIN_DESCRIPTIONS,
  ORIGIN_LABELS,
  STATE_LABELS,
  clipText,
  hasRedaction,
  jsonText,
  type DataOrigin,
} from '@/lib/httpInspector'
import { cn } from '@/lib/utils'
import type { HTTPExchangeKind, HTTPExchangeState } from '@/types/httpInspector'

// Building blocks of the HTTP Inspector's views. Captured payloads are
// untrusted: TextBlock and JsonBlock are the only components that show them,
// as plain text in a <pre> (React escapes it). Never render payload content
// as HTML, Markdown or a link.

interface TextBlockProps {
  text: string
  // Accessible name of the block and of its copy button, e.g. "Corpo da requisição"
  label: string
  className?: string
  // Characters rendered before "Mostrar tudo"
  maxChars?: number
}

/** Untrusted text shown literally, with a copy button and a redaction badge */
export function TextBlock({ text, label, className, maxChars }: TextBlockProps) {
  const [showAll, setShowAll] = useState(false)
  const clipped = clipText(text, maxChars)
  const shown = showAll ? text : clipped.text
  return (
    <div className={cn('relative min-w-0 rounded-md border bg-muted/40', className)}>
      <div className="flex items-center justify-end gap-1 border-b px-2 py-1">
        {hasRedaction(text) && <RedactedBadge />}
        <CopyButton value={text} label={`Copiar ${label.toLowerCase()}`} />
      </div>
      <pre
        aria-label={label}
        className="max-h-[32rem] overflow-auto p-3 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap"
      >
        {shown}
      </pre>
      {clipped.clipped && !showAll && (
        <div className="flex flex-wrap items-center gap-2 border-t px-3 py-2 text-xs text-muted-foreground">
          Mostrando {formatCount(clipped.text.length)} de {formatCount(text.length)} caracteres.
          <Button type="button" variant="outline" size="sm" onClick={() => setShowAll(true)}>
            Mostrar tudo
          </Button>
        </div>
      )}
    </div>
  )
}

/** A value as indented JSON text (see TextBlock) */
export function JsonBlock({ value, label, className }: { value: unknown; label: string; className?: string }) {
  return <TextBlock text={jsonText(value)} label={label} className={className} />
}

/** "segredo mascarado": the text contains [REDACTED:...] markers */
export function RedactedBadge({ count, className }: { count?: number; className?: string }) {
  return (
    <Badge variant="warning" className={cn('gap-1', className)} title="Valores sensíveis foram substituídos por [REDACTED:tipo] antes de gravar">
      <ShieldAlert className="h-3 w-3" aria-hidden="true" />
      {count && count > 0 ? `${formatCount(count)} ${count === 1 ? 'valor mascarado' : 'valores mascarados'}` : 'segredo mascarado'}
    </Badge>
  )
}

/** Where a value comes from: wire, otel, jsonl or derivado */
export function OriginBadge({ origin, className }: { origin: DataOrigin; className?: string }) {
  return (
    <Badge
      variant="outline"
      className={cn('font-mono text-[10px] uppercase tracking-wide', origin === 'derivado' && 'border-dashed', className)}
      title={ORIGIN_DESCRIPTIONS[origin]}
    >
      <span className="sr-only">origem: </span>
      {ORIGIN_LABELS[origin]}
    </Badge>
  )
}

/** An HTTP status code; 0 (no response) as "sem resposta" */
export function HttpStatusBadge({ status, className }: { status: number; className?: string }) {
  if (!status) {
    return (
      <Badge variant="destructive" className={className}>
        sem resposta
      </Badge>
    )
  }
  const variant = status >= 500 ? 'destructive' : status >= 400 ? 'warning' : status >= 300 ? 'secondary' : 'success'
  return (
    <Badge variant={variant} className={cn('font-mono tabular-nums', className)} title={`HTTP ${status}`}>
      {status}
    </Badge>
  )
}

export function KindBadge({ kind, className }: { kind: HTTPExchangeKind; className?: string }) {
  return (
    <Badge variant={kind === 'transport_error' ? 'destructive' : kind === 'model_inference' ? 'secondary' : 'outline'} className={className}>
      {KIND_LABELS[kind] ?? kind}
    </Badge>
  )
}

/** The state of an exchange; a complete one only when always is set */
export function StateBadge({ state, always = false, className }: { state: HTTPExchangeState; always?: boolean; className?: string }) {
  if (state === 'complete' && !always) return null
  const variant = state === 'complete' ? 'success' : state === 'in_progress' ? 'secondary' : state === 'partial' ? 'warning' : 'destructive'
  return (
    <Badge variant={variant} className={className}>
      {STATE_LABELS[state] ?? state}
    </Badge>
  )
}

/** A value that was not reported ("ausente"), unlike 0 */
export function Absent({ children = 'ausente', title }: { children?: ReactNode; title?: string }) {
  return (
    <span className="text-muted-foreground italic" title={title ?? 'Não informado na captura (diferente de zero)'}>
      {children}
    </span>
  )
}

/** A label and its value, in a definition list (Fields) */
export interface FieldItem {
  label: ReactNode
  value: ReactNode
  // Shown under the value, e.g. the formula or origin
  hint?: ReactNode
  key?: string
}

/** Label/value pairs in a grid */
export function Fields({ items, className, label }: { items: FieldItem[]; className?: string; label?: string }) {
  return (
    <dl aria-label={label} className={cn('grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3', className)}>
      {items.map((item, index) => (
        <div key={item.key ?? (typeof item.label === 'string' ? item.label : index)} className="min-w-0">
          <dt className="text-xs text-muted-foreground">{item.label}</dt>
          <dd className="min-w-0 font-medium break-words tabular-nums">{item.value}</dd>
          {item.hint && <dd className="text-xs text-muted-foreground">{item.hint}</dd>}
        </div>
      ))}
    </dl>
  )
}

interface ExpandableProps {
  title: ReactNode
  children: ReactNode
  defaultOpen?: boolean
  className?: string
}

/** A section whose content is rendered only while open */
export function Expandable({ title, children, defaultOpen = false, className }: ExpandableProps) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <Collapsible open={open} onOpenChange={setOpen} className={cn('rounded-md border', className)}>
      <CollapsibleTrigger className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent/50">
        <ChevronRight className={cn('h-4 w-4 shrink-0 transition-transform', open && 'rotate-90')} aria-hidden="true" />
        <span className="min-w-0 flex-1">{title}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-3 border-t p-3">{children}</CollapsibleContent>
    </Collapsible>
  )
}

/** A titled block of a tab */
export function Section({ title, actions, children, className }: { title: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cn('space-y-3', className)}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-base font-semibold">{title}</h3>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  )
}
