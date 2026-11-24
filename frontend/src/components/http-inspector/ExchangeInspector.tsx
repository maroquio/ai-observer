import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CopyButton } from '@/components/common/copy-button'
import { QueryError } from '@/components/common/query-error'
import { useDocumentTitle } from '@/hooks/useDocumentTitle'
import { useLiveInvalidate } from '@/hooks/useLiveInvalidate'
import { useUrlFilters, useUrlWriter } from '@/hooks/useUrlState'
import { isHTTPError } from '@/lib/errors'
import { CAPTURE_MODE_LABELS, endpointLabel, exchangePath, runPath, sessionTrafficPath, type ExchangeContext } from '@/lib/httpInspector'
import { queryKeys, useHTTPExchange } from '@/lib/queries'
import { formatTimestamp } from '@/lib/utils'
import { useTelemetryStore } from '@/stores/telemetryStore'
import { HttpStatusBadge, KindBadge, StateBadge } from './primitives'
import { DiffTab } from './inspector/DiffTab'
import { RequestTab } from './inspector/RequestTab'
import { ResponseTab } from './inspector/ResponseTab'
import { SseTab } from './inspector/SseTab'
import { SummaryTab } from './inspector/SummaryTab'
import { UsageTab } from './inspector/UsageTab'

const TABS = [
  { value: 'resumo', label: 'Resumo' },
  { value: 'request', label: 'Request' },
  { value: 'response', label: 'Response' },
  { value: 'sse', label: 'SSE Stream' },
  { value: 'tokens', label: 'Tokens & Cache' },
  { value: 'diff', label: 'Context Diff' },
] as const

type TabValue = (typeof TABS)[number]['value']

function isTab(value: string): value is TabValue {
  return TABS.some((t) => t.value === value)
}

interface ExchangeInspectorProps {
  exchangeId: string
  // Where the inspector was opened: links to other exchanges stay there
  context: ExchangeContext
  backButton: ReactNode
}

/**
 * The inspector of one exchange: a header and six tabs, the selected one
 * kept in the URL (?tab=), so a link or reload opens the same view
 */
export function ExchangeInspector({ exchangeId, context, backButton }: ExchangeInspectorProps) {
  const url = useUrlWriter()
  const [{ tab: tabParam }, setUrl] = useUrlFilters({ tab: 'resumo' }, url)
  const tab: TabValue = isTab(tabParam) ? tabParam : 'resumo'
  const query = useHTTPExchange(exchangeId)
  const exchange = query.data?.exchange

  // An exchange in progress gains its response: refresh it as the proxy
  // stores it. Finished exchanges do not change (their bodies can be large).
  const inProgress = exchange?.state === 'in_progress'
  const updateCount = useTelemetryStore((s) => s.httpExchangesUpdateCount)
  useLiveInvalidate({
    queryKey: queryKeys.httpInspector.exchange(exchangeId),
    enabled: inProgress,
    intervalMs: 5_000,
    updateCount,
    debounceMs: 500,
    maxWaitMs: 2_000,
  })

  useDocumentTitle(exchange ? `Troca #${exchange.seq} · HTTP Inspector` : 'HTTP Inspector')

  if (query.isPending) {
    return (
      <div className="space-y-6">
        {backButton}
        <div className="space-y-3" role="status" aria-busy="true" aria-label="Carregando troca">
          <Skeleton className="h-8 w-1/2" />
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-64 w-full" />
        </div>
      </div>
    )
  }
  if (query.isError || !query.data) {
    const notFound = isHTTPError(query.error, 404)
    return (
      <div className="space-y-6">
        {backButton}
        <QueryError
          error={notFound ? null : query.error}
          title={notFound ? 'Troca não encontrada' : 'Não foi possível carregar a troca'}
          fallback={notFound ? 'Esta troca não existe ou sua execução foi excluída.' : 'Falha ao carregar a troca.'}
          onRetry={notFound ? undefined : () => void query.refetch()}
          retrying={query.isFetching}
        />
      </div>
    )
  }

  const detail = query.data
  const ex = detail.exchange
  // Neighbours are of the run (the API orders a run's exchanges); they open
  // on the same tab
  const runContext: ExchangeContext = { runId: ex.runId }
  const neighbourContext = context.sessionId ? runContext : context
  const tabSuffix = tab === 'resumo' ? '' : `?tab=${tab}`

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {backButton}
        <nav aria-label="Trocas vizinhas na execução" className="flex items-center gap-2">
          <NeighbourLink to={detail.prevId ? exchangePath(neighbourContext, detail.prevId) + tabSuffix : undefined} label="Troca anterior da execução">
            <ChevronLeft className="h-4 w-4" aria-hidden="true" /> Anterior
          </NeighbourLink>
          <NeighbourLink to={detail.nextId ? exchangePath(neighbourContext, detail.nextId) + tabSuffix : undefined} label="Próxima troca da execução">
            Próxima <ChevronRight className="h-4 w-4" aria-hidden="true" />
          </NeighbourLink>
        </nav>
      </div>

      <Card>
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold">Troca #{ex.seq}</h1>
            <HttpStatusBadge status={ex.status} />
            <KindBadge kind={ex.kind} />
            <StateBadge state={ex.state} always />
            {ex.degraded && <Badge variant="warning">captura degradada</Badge>}
            <Badge variant="outline" title="Modo de captura">
              {CAPTURE_MODE_LABELS[ex.captureMode] ?? ex.captureMode}
            </Badge>
          </div>
          <p className="text-base">{ex.headline}</p>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
            <span className="font-mono text-xs">{endpointLabel(ex)}</span>
            <span>{formatTimestamp(ex.startedAt)}</span>
            {ex.sessionId ? (
              <Link to={sessionTrafficPath(ex.sessionId)} className="underline-offset-4 hover:underline">
                Sessão <span className="font-mono text-xs">{ex.sessionId}</span>
              </Link>
            ) : (
              <span>Sem sessão (não correlacionada)</span>
            )}
            <Link to={runPath(ex.runId)} className="underline-offset-4 hover:underline">
              Execução: {detail.run.name || ex.runId}
            </Link>
            <span className="flex items-center gap-1 font-mono text-xs">
              {ex.id}
              <CopyButton value={ex.id} label="Copiar ID da troca" />
            </span>
          </div>
        </CardHeader>
        <CardContent>
          <Tabs value={tab} onValueChange={(value) => setUrl({ tab: value })} className="gap-4">
            <div className="overflow-x-auto">
              <TabsList aria-label="Partes da troca">
                {TABS.map((t) => (
                  <TabsTrigger key={t.value} value={t.value}>
                    {t.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>
            <TabsContent value="resumo">
              <SummaryTab detail={detail} />
            </TabsContent>
            <TabsContent value="request">
              <RequestTab exchangeId={ex.id} />
            </TabsContent>
            <TabsContent value="response">
              <ResponseTab exchangeId={ex.id} />
            </TabsContent>
            <TabsContent value="sse">
              <SseTab exchange={ex} />
            </TabsContent>
            <TabsContent value="tokens">
              <UsageTab exchangeId={ex.id} />
            </TabsContent>
            <TabsContent value="diff">
              <DiffTab exchangeId={ex.id} context={context.sessionId ? context : runContext} />
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>
    </div>
  )
}

function NeighbourLink({ to, label, children }: { to?: string; label: string; children: ReactNode }) {
  if (!to) {
    return (
      <Button variant="outline" size="sm" disabled aria-label={label} className="gap-1">
        {children}
      </Button>
    )
  }
  return (
    <Button variant="outline" size="sm" asChild className="gap-1">
      <Link to={to} aria-label={label} title={label}>
        {children}
      </Link>
    </Button>
  )
}
