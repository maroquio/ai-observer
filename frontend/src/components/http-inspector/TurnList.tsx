import { useMemo } from 'react'
import { ChevronDown, ChevronRight, MessageSquare } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { formatCost, formatCount, formatDurationMs } from '@/lib/format'
import { COST_STATUS_LABELS, isAuxiliaryExchange, toolUseCounts } from '@/lib/httpInspector'
import type { ListLinkState } from '@/lib/listLinks'
import { formatTimestamp } from '@/lib/utils'
import type { HTTPExchange, HTTPTurn } from '@/types/httpInspector'
import { ExchangeTimeline } from './ExchangeTimeline'

interface TurnCardProps {
  turn: HTTPTurn
  open: boolean
  onOpenChange: (open: boolean) => void
  // Show count_tokens, quota probes and other non-inference exchanges
  showAuxiliary: boolean
  linkFor: (exchange: HTTPExchange) => string
  linkState: ListLinkState
}

/**
 * A turn of a session's captured traffic: the typed prompt (as text, never
 * rendered as markup), what the turn cost and, when open, its exchanges.
 * Index 0 is the preamble, the exchanges before the first prompt.
 */
export function TurnCard({ turn, open, onOpenChange, showAuxiliary, linkFor, linkState }: TurnCardProps) {
  const preamble = turn.index === 0
  const label = preamble ? 'Antes do primeiro prompt' : `Turno ${turn.index}`
  const shown = useMemo(() => (showAuxiliary ? turn.exchanges : turn.exchanges.filter((e) => !isAuxiliaryExchange(e))), [turn.exchanges, showAuxiliary])
  const tools = useMemo(() => toolUseCounts(turn.toolUses), [turn.toolUses])
  const tokens = turn.tokens
  const hidden = turn.exchanges.length - shown.length

  return (
    <Collapsible open={open} onOpenChange={onOpenChange} asChild>
      <Card className="gap-0 py-0" role="article" aria-label={label}>
        <div className="space-y-3 p-4">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <CollapsibleTrigger className="-ml-1 inline-flex items-center gap-1 rounded px-1 font-semibold outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/50">
              {open ? <ChevronDown className="h-4 w-4" aria-hidden="true" /> : <ChevronRight className="h-4 w-4" aria-hidden="true" />}
              {label}
            </CollapsibleTrigger>
            <span className="text-xs text-muted-foreground">{formatTimestamp(turn.startedAt)}</span>
            <span className="text-xs text-muted-foreground">{formatDurationMs(turn.durationMs)}</span>
            {turn.errors > 0 && (
              <Badge variant="warning">
                {formatCount(turn.errors)} {turn.errors === 1 ? 'erro' : 'erros'}
              </Badge>
            )}
          </div>

          {!preamble &&
            (turn.prompt ? (
              <p className="flex gap-2 text-sm" aria-label="Prompt do turno">
                <MessageSquare className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span className="line-clamp-4 min-w-0 whitespace-pre-wrap break-words">{turn.prompt}</span>
              </p>
            ) : (
              <p className="text-sm text-muted-foreground">
                Prompt não registrado (captura em modo metadata_only, ou feita por uma versão anterior).
              </p>
            ))}

          <dl className="flex flex-wrap gap-x-5 gap-y-1 text-xs">
            <Stat label="Inferências" value={formatCount(turn.inference)} />
            <Stat label="Chamadas auxiliares" value={formatCount(turn.auxiliary)} />
            {tokens.exchanges > 0 && (
              <>
                <Stat label="Entrada (total)" value={formatCount(tokens.totalInput)} />
                <Stat label="Saída" value={formatCount(tokens.output)} />
                <Stat label="Leitura de cache" value={formatCount(tokens.cacheRead)} />
                <Stat label="Escrita de cache" value={formatCount(tokens.cacheWrite)} />
              </>
            )}
            {turn.partialTokens.exchanges > 0 && <Stat label="Usage parcial" value={`${formatCount(turn.partialTokens.exchanges)} trocas`} />}
            <Stat
              label="Custo estimado"
              value={turn.costStatus === 'unavailable' ? '—' : `${formatCost(turn.costUsd)}${turn.costStatus === 'partial' ? ` (${COST_STATUS_LABELS.partial})` : ''}`}
            />
            {turn.models.length > 0 && <Stat label="Modelo" value={turn.models.join(', ')} />}
          </dl>

          {tools.length > 0 && (
            <div className="flex flex-wrap items-center gap-1 text-xs" aria-label="Ferramentas solicitadas">
              <span className="text-muted-foreground">Ferramentas:</span>
              {tools.map((t) => (
                <Badge key={t.name} variant="outline" className="font-mono text-[11px]">
                  {t.name}
                  {t.count > 1 && ` ×${t.count}`}
                </Badge>
              ))}
            </div>
          )}
        </div>

        <CollapsibleContent className="border-t px-4 pb-4 pt-3">
          {shown.length > 0 ? (
            <ExchangeTimeline exchanges={shown} linkFor={linkFor} linkState={linkState} virtualize={false} />
          ) : (
            <p className="py-2 text-sm text-muted-foreground">Só chamadas auxiliares neste turno.</p>
          )}
          {hidden > 0 && (
            <p className="mt-2 text-xs text-muted-foreground">
              {formatCount(hidden)} {hidden === 1 ? 'chamada auxiliar oculta' : 'chamadas auxiliares ocultas'} (count_tokens, sonda de cota, outros
              endpoints).
            </p>
          )}
        </CollapsibleContent>
      </Card>
    </Collapsible>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-1">
      <dt className="text-muted-foreground">{label}:</dt>
      <dd className="font-medium tabular-nums">{value}</dd>
    </div>
  )
}
