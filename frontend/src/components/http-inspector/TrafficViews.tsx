import { useMemo } from 'react'
import { SearchX } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { EmptyState } from '@/components/common/empty-state'
import { ListSkeleton, Refreshing } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { useListLinkState } from '@/hooks/useListLinkState'
import { errorText } from '@/lib/errors'
import { formatCount } from '@/lib/format'
import { hasExchangeFilters, type ExchangeFilterValues } from '@/lib/httpInspector'
import type { HTTPCaptureRunMode, HTTPExchange, HTTPExchangesResponse, HTTPToolLink } from '@/types/httpInspector'
import { ExchangeFilters } from './ExchangeFilters'
import { ExchangeTimeline } from './ExchangeTimeline'
import { ToolLinks } from './ToolLinks'

// The infinite exchange query of a session or run (useSessionExchanges,
// useHTTPRunExchanges)
interface ExchangeListQuery {
  data?: { pages: HTTPExchangesResponse[] }
  isPending: boolean
  isError: boolean
  error: unknown
  isFetching: boolean
  isPlaceholderData: boolean
  hasNextPage: boolean
  isFetchingNextPage: boolean
  isFetchNextPageError: boolean
  fetchNextPage: () => unknown
  refetch: () => unknown
}

interface ToolLinksQuery {
  tools: HTTPToolLink[]
  note: string
  isPending: boolean
  isError: boolean
  error: unknown
  isFetching: boolean
  refetch: () => unknown
}

interface TrafficViewsProps {
  section: string
  onSectionChange: (section: string) => void
  filters: ExchangeFilterValues
  onFiltersChange: (updates: Partial<ExchangeFilterValues>) => void
  exchanges: ExchangeListQuery
  tools: ToolLinksQuery
  linkFor: (exchange: HTTPExchange) => string
  exchangePathFor: (exchangeId: string) => string
  runModes?: Map<string, HTTPCaptureRunMode>
  showCorrelation?: boolean
}

/** The exchange timeline (with its filters) and the tool links of a session or run, as tabs */
export function TrafficViews({
  section,
  onSectionChange,
  filters,
  onFiltersChange,
  exchanges,
  tools,
  linkFor,
  exchangePathFor,
  runModes,
  showCorrelation,
}: TrafficViewsProps) {
  const linkState = useListLinkState()
  const rows = useMemo(() => exchanges.data?.pages.flatMap((page) => page.exchanges ?? []) ?? [], [exchanges.data])
  const pages = exchanges.data?.pages
  const total = pages?.[pages.length - 1]?.total ?? rows.length
  const filtered = hasExchangeFilters(filters)

  let timeline
  if (exchanges.isPending) {
    timeline = <ListSkeleton rows={6} rowClassName="h-16" label="Carregando trocas" />
  } else if (exchanges.isError && !exchanges.data) {
    timeline = (
      <QueryError error={exchanges.error} title="Não foi possível carregar as trocas" onRetry={() => void exchanges.refetch()} retrying={exchanges.isFetching} />
    )
  } else if (rows.length === 0) {
    timeline = filtered ? (
      <EmptyState
        icon={SearchX}
        title="Nenhuma troca corresponde aos filtros"
        action={
          <Button type="button" variant="outline" size="sm" onClick={() => onFiltersChange({ kind: '', status: '', state: '', search: '' })}>
            Limpar filtros
          </Button>
        }
      />
    ) : (
      <EmptyState title="Nenhuma troca" />
    )
  } else {
    timeline = (
      <Refreshing active={exchanges.isPlaceholderData}>
        <p className="mb-2 text-xs text-muted-foreground" role="status">
          {formatCount(rows.length)} de {formatCount(total)} {total === 1 ? 'troca' : 'trocas'}
          {filtered && ' que correspondem aos filtros'}
        </p>
        <ExchangeTimeline exchanges={rows} linkFor={linkFor} linkState={linkState} runModes={runModes} showCorrelation={showCorrelation} />
        {exchanges.hasNextPage && (
          <div className="mt-4 flex flex-col items-center gap-2">
            {exchanges.isFetchNextPageError && (
              <span className="text-sm text-error" role="alert">
                {errorText(exchanges.error, 'Falha ao carregar mais trocas')}
              </span>
            )}
            <Button type="button" variant="outline" size="sm" onClick={() => void exchanges.fetchNextPage()} disabled={exchanges.isFetchingNextPage}>
              {exchanges.isFetchingNextPage ? 'Carregando…' : 'Carregar mais trocas'}
            </Button>
          </div>
        )}
      </Refreshing>
    )
  }

  return (
    <Tabs value={section === 'tools' ? 'tools' : 'timeline'} onValueChange={onSectionChange} className="gap-4">
      <TabsList aria-label="Visões do tráfego">
        <TabsTrigger value="timeline">Linha do tempo</TabsTrigger>
        <TabsTrigger value="tools">Ferramentas (tool_use → tool_result)</TabsTrigger>
      </TabsList>
      <TabsContent value="timeline" className="space-y-4">
        <ExchangeFilters values={filters} onChange={onFiltersChange} />
        {timeline}
      </TabsContent>
      <TabsContent value="tools">
        {tools.isPending ? (
          <ListSkeleton rows={4} label="Carregando ferramentas" />
        ) : tools.isError ? (
          <QueryError error={tools.error} title="Não foi possível carregar as ferramentas" onRetry={() => void tools.refetch()} retrying={tools.isFetching} />
        ) : (
          <ToolLinks tools={tools.tools} note={tools.note} exchangePathFor={exchangePathFor} />
        )}
      </TabsContent>
    </Tabs>
  )
}
