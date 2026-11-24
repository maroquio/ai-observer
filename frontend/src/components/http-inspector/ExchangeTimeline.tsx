import { memo } from 'react'
import { Link } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { formatCount } from '@/lib/format'
import {
  endpointLabel,
  exchangeModel,
  exchangeTokens,
  expectsUsage,
  formatClock,
  formatMaybeMs,
  linkQuality,
  LINK_QUALITY_LABELS,
} from '@/lib/httpInspector'
import type { ListLinkState } from '@/lib/listLinks'
import { cn, formatTimestamp } from '@/lib/utils'
import type { HTTPCaptureRunMode, HTTPExchange } from '@/types/httpInspector'
import { Absent, HttpStatusBadge, KindBadge, StateBadge } from './primitives'
import { VirtualRows } from './VirtualRows'

// Rows have one height, so thousands of exchanges virtualize without
// measuring them
const ROW_HEIGHT = 76

const GRID = 'grid grid-cols-[3rem_5.5rem_8.5rem_minmax(16rem,1fr)_6.5rem_9rem_5rem_6.5rem_8rem] gap-3'

interface ExchangeTimelineProps {
  exchanges: HTTPExchange[]
  // The inspector path of an exchange
  linkFor: (exchange: HTTPExchange) => string
  linkState: ListLinkState
  // Modes of the exchanges' runs, to tell launcher runs from uncorrelated
  // exchanges; set where exchanges of several sessions are listed (a run)
  runModes?: Map<string, HTTPCaptureRunMode>
  // Labels exchanges without a session (a run's timeline)
  showCorrelation?: boolean
}

/**
 * The exchanges of a session or run, one row each, in arrival order:
 * also count_tokens requests, errors and other endpoints. A row opens the
 * exchange's inspector (its own URL).
 */
export function ExchangeTimeline({ exchanges, linkFor, linkState, runModes, showCorrelation = false }: ExchangeTimelineProps) {
  return (
    <div className="overflow-x-auto">
      <div role="table" aria-label="Linha do tempo das trocas" aria-rowcount={exchanges.length + 1} className="min-w-[68rem]">
        <div role="rowgroup">
          <div role="row" aria-rowindex={1} className={cn(GRID, 'border-b px-3 pb-2 text-xs font-medium text-muted-foreground')}>
            <div role="columnheader">#</div>
            <div role="columnheader">Status</div>
            <div role="columnheader">Tipo</div>
            <div role="columnheader">Endpoint e descrição</div>
            <div role="columnheader">Horário</div>
            <div role="columnheader">Modelo</div>
            <div role="columnheader">Duração</div>
            <div role="columnheader">Tokens</div>
            <div role="columnheader">Cache</div>
          </div>
        </div>
        <VirtualRows
          role="rowgroup"
          count={exchanges.length}
          rowHeight={ROW_HEIGHT}
          rowKey={(index) => exchanges[index].id}
          renderRow={(index) => (
            <TimelineRow
              exchange={exchanges[index]}
              rowIndex={index + 2}
              to={linkFor(exchanges[index])}
              linkState={linkState}
              runMode={runModes?.get(exchanges[index].runId)}
              showCorrelation={showCorrelation}
            />
          )}
        />
      </div>
    </div>
  )
}

interface TimelineRowProps {
  exchange: HTTPExchange
  rowIndex: number
  to: string
  linkState: ListLinkState
  runMode?: HTTPCaptureRunMode
  showCorrelation: boolean
}

const TimelineRow = memo(function TimelineRow({ exchange, rowIndex, to, linkState, runMode, showCorrelation }: TimelineRowProps) {
  const endpoint = endpointLabel(exchange)
  const model = exchangeModel(exchange)
  const tokens = exchangeTokens(exchange)
  const usage = exchange.usage
  const duration = formatMaybeMs(exchange.durationMs)
  const quality = linkQuality(exchange, runMode)

  const flags: Array<{ label: string; tone?: 'warning' | 'destructive'; title?: string }> = []
  if (exchange.degraded) flags.push({ label: 'captura degradada', tone: 'warning', title: 'Parte desta troca não foi capturada (o tráfego não foi afetado)' })
  if (exchange.retryCount > 0) flags.push({ label: `retry ${exchange.retryCount}`, tone: 'warning', title: 'X-Stainless-Retry-Count do cliente' })
  if (usage?.quality === 'partial') flags.push({ label: 'usage parcial', tone: 'warning' })
  if (expectsUsage(exchange.kind) && (!usage || usage.quality === 'unavailable')) flags.push({ label: 'usage indisponível' })
  if (showCorrelation && quality === 'none') flags.push({ label: LINK_QUALITY_LABELS.none.toLowerCase(), title: 'Nenhuma evidência liga esta troca a uma sessão' })
  if (showCorrelation && quality === 'launcher') flags.push({ label: 'só launcher', title: 'Sem ID de sessão; ligada apenas à execução de ai-observer run' })
  if (exchange.captureMode === 'metadata_only') flags.push({ label: 'só metadados' })
  if (exchange.stream) flags.push({ label: 'SSE' })
  if (exchange.toolUses.length > 0) flags.push({ label: `tool_use: ${exchange.toolUses.join(', ')}` })
  if (exchange.toolResults > 0) flags.push({ label: `${formatCount(exchange.toolResults)} tool_result` })

  return (
    <div
      role="row"
      aria-rowindex={rowIndex}
      className={cn(GRID, 'relative h-full items-center border-b px-3 text-sm hover:bg-accent/50 focus-within:bg-accent/50')}
    >
      <div role="cell" className="font-mono text-xs text-muted-foreground tabular-nums">
        {exchange.seq}
      </div>
      <div role="cell">
        <HttpStatusBadge status={exchange.status} />
      </div>
      <div role="cell" className="flex flex-col items-start gap-1">
        <KindBadge kind={exchange.kind} />
        <StateBadge state={exchange.state} />
      </div>
      <div role="cell" className="min-w-0 space-y-0.5">
        <Link
          to={to}
          state={linkState}
          aria-label={`Troca #${exchange.seq}: ${endpoint}`}
          className="block truncate font-mono text-xs font-medium outline-none after:absolute after:inset-0 after:content-['']"
          title={endpoint}
        >
          {endpoint}
        </Link>
        <p className="truncate text-xs text-muted-foreground" title={exchange.headline}>
          {exchange.headline}
        </p>
        {flags.length > 0 && (
          <div className="flex min-w-0 gap-1 overflow-hidden">
            {flags.map((flag) => (
              <Badge
                key={flag.label}
                variant={flag.tone ?? 'outline'}
                className="shrink-0 px-1.5 py-0 text-[10px] font-medium"
                title={flag.title}
              >
                {flag.label}
              </Badge>
            ))}
          </div>
        )}
      </div>
      <div role="cell" className="font-mono text-xs tabular-nums" title={formatTimestamp(exchange.startedAt)}>
        {formatClock(exchange.startedAt)}
      </div>
      <div role="cell" className="truncate text-xs" title={model}>
        {model ?? '—'}
      </div>
      <div role="cell" className="text-xs tabular-nums">
        {duration ?? <Absent title="A troca não terminou">—</Absent>}
      </div>
      <div role="cell" className="text-xs tabular-nums">
        {tokens ? (
          <span title={tokens.estimate ? 'Estimativa de count_tokens (não é consumo)' : 'Tokens processados (entrada total + saída)'}>
            {tokens.estimate ? '≈ ' : ''}
            {formatCount(tokens.value)}
          </span>
        ) : expectsUsage(exchange.kind) ? (
          <Absent />
        ) : (
          '—'
        )}
      </div>
      <div role="cell" className="text-xs tabular-nums">
        <CacheCell exchange={exchange} />
      </div>
    </div>
  )
})

function CacheCell({ exchange }: { exchange: HTTPExchange }) {
  const usage = exchange.usage?.usage
  // A field the API did not report is absent (null or left out), not 0
  if (!usage || (usage.cacheReadInputTokens == null && usage.cacheCreationInputTokens == null)) {
    return expectsUsage(exchange.kind) && exchange.usage ? <Absent /> : <>—</>
  }
  const split = [
    usage.cacheCreation5m != null ? `5m: ${formatCount(usage.cacheCreation5m)}` : null,
    usage.cacheCreation1h != null ? `1h: ${formatCount(usage.cacheCreation1h)}` : null,
  ].filter(Boolean)
  return (
    <div className="space-y-0.5" title={split.length > 0 ? `Escrita de cache por duração — ${split.join(', ')}` : undefined}>
      <div>leitura {usage.cacheReadInputTokens != null ? formatCount(usage.cacheReadInputTokens) : <Absent />}</div>
      <div>escrita {usage.cacheCreationInputTokens != null ? formatCount(usage.cacheCreationInputTokens) : <Absent />}</div>
    </div>
  )
}
