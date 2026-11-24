import { useCallback, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { useDebounce } from '@/hooks/useDebounce'
import { useUrlFilters, type UrlWriter } from '@/hooks/useUrlState'
import { formatCount } from '@/lib/format'
import {
  EXCHANGE_FILTER_DEFAULTS,
  RUN_MODE_LABELS,
  RUN_STATE_LABELS,
  exchangeFiltersFromValues,
  exchangePath,
  hasExchangeFilters,
  inspectorSessionPath,
  runPath,
} from '@/lib/httpInspector'
import { useSessionExchanges, useSessionToolLinks, useSessionTrafficSummary } from '@/lib/queries'
import type { HTTPExchange } from '@/types/httpInspector'
import { CaptureHelp } from './CaptureHelp'
import { TrafficSummary } from './TrafficSummary'
import { TrafficViews } from './TrafficViews'

// Typing pauses this long before the exchanges are searched
const SEARCH_DEBOUNCE_MS = 300

// The tab's own URL parameters, besides the exchange filters
const SECTION_DEFAULTS = { section: 'timeline' }

interface SessionTrafficTabProps {
  sessionId: string
  // The page's URL writer: the tab's filters share the URL with the page's tab
  url: UrlWriter
}

/**
 * "Tráfego da API" of a session: what the HTTP Inspector's proxy captured
 * of the session's requests to the Anthropic API. Only real, linked
 * exchanges are listed; without any, how to capture them.
 */
export function SessionTrafficTab({ sessionId, url }: SessionTrafficTabProps) {
  const [values, setValues] = useUrlFilters(EXCHANGE_FILTER_DEFAULTS, url)
  const [{ section }, setSection] = useUrlFilters(SECTION_DEFAULTS, url)
  const search = useDebounce(values.search, SEARCH_DEBOUNCE_MS)
  const filters = useMemo(() => exchangeFiltersFromValues(values, search), [values, search])

  const summaryQuery = useSessionTrafficSummary(sessionId)
  const captured = (summaryQuery.data?.summary.exchanges ?? 0) > 0
  const runs = useMemo(() => summaryQuery.data?.runs ?? [], [summaryQuery.data])
  const exchanges = useSessionExchanges(sessionId, filters, { enabled: captured })
  const tools = useSessionToolLinks(
    sessionId,
    runs.map((r) => r.id),
    { enabled: captured && section === 'tools' },
  )

  const linkFor = useCallback((exchange: HTTPExchange) => exchangePath({ sessionId }, exchange.id), [sessionId])
  const exchangePathFor = useCallback((id: string) => exchangePath({ sessionId }, id), [sessionId])
  const runModes = useMemo(() => new Map(runs.map((r) => [r.id, r.mode])), [runs])
  const loaded = useMemo(() => exchanges.data?.pages.flatMap((p) => p.exchanges ?? []) ?? [], [exchanges.data])

  if (summaryQuery.isPending) {
    return <ListSkeleton rows={4} rowClassName="h-24" label="Carregando tráfego da API" />
  }
  if (summaryQuery.isError) {
    return (
      <QueryError
        error={summaryQuery.error}
        title="Não foi possível carregar o tráfego da API"
        onRetry={() => void summaryQuery.refetch()}
        retrying={summaryQuery.isFetching}
      />
    )
  }
  const traffic = summaryQuery.data
  if (!captured) {
    return <CaptureHelp title="Nenhum tráfego da API capturado para esta sessão" />
  }

  return (
    <div className="space-y-6">
      <TrafficSummary
        summary={traffic.summary}
        comparison={traffic.comparison}
        showComparison
        exchanges={loaded}
        allLoaded={!exchanges.hasNextPage && !hasExchangeFilters(values)}
        runs={runs}
      />

      <p className="text-sm">
        <Link to={inspectorSessionPath(sessionId)} className="font-medium underline underline-offset-4">
          Ver por turnos no HTTP Inspector
        </Link>
        <span className="text-muted-foreground"> — cada prompt com as requisições e respostas que vieram dele</span>
      </p>

      {runs.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">{runs.length === 1 ? 'Execução de captura:' : 'Execuções de captura:'}</span>
          {runs.map((run) => (
            <Link key={run.id} to={runPath(run.id)} className="inline-flex items-center gap-1 underline-offset-4 hover:underline">
              {run.name || run.id}
              <Badge variant="outline" className="text-[10px]" title={RUN_MODE_LABELS[run.mode]}>
                {RUN_STATE_LABELS[run.state] ?? run.state}
              </Badge>
              <span className="text-xs text-muted-foreground">({formatCount(run.exchanges)})</span>
            </Link>
          ))}
        </div>
      )}

      <TrafficViews
        section={section}
        onSectionChange={(next) => setSection({ section: next })}
        filters={values}
        onFiltersChange={setValues}
        exchanges={exchanges}
        tools={tools}
        linkFor={linkFor}
        exchangePathFor={exchangePathFor}
        runModes={runModes}
      />
    </div>
  )
}
