import { useCallback, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { AlertTriangle, ArrowLeft, Info } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { CopyButton } from '@/components/common/copy-button'
import { EmptyState } from '@/components/common/empty-state'
import { PageHeader } from '@/components/common/page-header'
import { QueryError } from '@/components/common/query-error'
import { TrafficSummary } from '@/components/http-inspector/TrafficSummary'
import { TurnCard } from '@/components/http-inspector/TurnList'
import { useBackLink } from '@/hooks/useBackLink'
import { useListLinkState } from '@/hooks/useListLinkState'
import { useLiveInvalidate } from '@/hooks/useLiveInvalidate'
import { isHTTPError } from '@/lib/errors'
import { formatCount } from '@/lib/format'
import {
  HTTP_INSPECTOR_PATH,
  RUN_MODE_LABELS,
  RUN_STATE_LABELS,
  exchangePath,
  runPath,
  sessionIdFromParam,
  sessionTrafficPath,
  shortSessionId,
} from '@/lib/httpInspector'
import { queryKeys, useHTTPSessionTurns } from '@/lib/queries'
import { useTelemetryStore } from '@/stores/telemetryStore'
import type { HTTPExchange, HTTPTurn } from '@/types/httpInspector'

/**
 * /http-inspector/sessions/:sessionId ("none": the exchanges without a
 * session): a session's captured traffic as turns, each a typed prompt with
 * the requests and responses that followed it
 */
export function HttpSessionPage() {
  const { sessionId: param = '' } = useParams<{ sessionId: string }>()
  const sessionId = sessionIdFromParam(param)
  const sessionless = sessionId === ''
  const query = useHTTPSessionTurns(sessionId)
  const back = useBackLink(HTTP_INSPECTOR_PATH)
  const linkState = useListLinkState()
  const [showAuxiliary, setShowAuxiliary] = useState(false)
  // Open turns by index; the preamble is index 0 (open when there are no
  // turns: the exchanges without a session)
  const [open, setOpen] = useState<Set<number>>(() => new Set(sessionless ? [0] : []))

  const updateCount = useTelemetryStore((s) => s.httpExchangesUpdateCount)
  useLiveInvalidate({
    queryKey: queryKeys.httpInspector.sessionTurns(sessionId),
    enabled: true,
    intervalMs: 30_000,
    updateCount,
    debounceMs: 1_000,
    maxWaitMs: 5_000,
  })

  const data = query.data
  const groups = useMemo<HTTPTurn[]>(() => (data ? [...(data.preamble ? [data.preamble] : []), ...data.turns] : []), [data])
  const allExchanges = useMemo(() => groups.flatMap((t) => t.exchanges), [groups])
  const linkFor = useCallback(
    (exchange: HTTPExchange) => (sessionless ? exchangePath({ runId: exchange.runId }, exchange.id) : exchangePath({ sessionId }, exchange.id)),
    [sessionId, sessionless],
  )
  const toggle = (index: number, next: boolean) =>
    setOpen((prev) => {
      const out = new Set(prev)
      if (next) out.add(index)
      else out.delete(index)
      return out
    })

  const title = sessionless ? 'Sem sessão' : `Sessão ${shortSessionId(sessionId)}`
  const backButton = (
    <Button variant="ghost" size="sm" className="-ml-2 gap-2" asChild>
      <Link to={back.to}>
        <ArrowLeft className="h-4 w-4" aria-hidden="true" />
        {back.label}
      </Link>
    </Button>
  )

  if (query.isPending) {
    return (
      <div className="space-y-6">
        {backButton}
        <div className="space-y-3" role="status" aria-busy="true" aria-label="Carregando sessão">
          <Skeleton className="h-8 w-1/2" />
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      </div>
    )
  }
  if (query.isError || !data) {
    const notFound = isHTTPError(query.error, 404)
    return (
      <div className="space-y-6">
        {backButton}
        <PageHeader title={title} documentTitle={`${title} · HTTP Inspector`} />
        <QueryError
          error={notFound ? null : query.error}
          title={notFound ? 'Nenhum tráfego capturado' : 'Não foi possível carregar a sessão'}
          fallback={notFound ? 'O HTTP Inspector não capturou requisições desta sessão.' : 'Falha ao carregar a sessão.'}
          onRetry={notFound ? undefined : () => void query.refetch()}
          retrying={query.isFetching}
        />
      </div>
    )
  }

  const firstPrompt = data.turns.find((t) => t.prompt)?.prompt
  return (
    <div className="space-y-6">
      {backButton}
      <PageHeader
        title={title}
        documentTitle={`${title} · HTTP Inspector`}
        description={
          sessionless
            ? 'Requisições que chegaram ao proxy sem ID de sessão (checagens de conectividade, outros endpoints)'
            : `${formatCount(data.turns.length)} ${data.turns.length === 1 ? 'turno' : 'turnos'} · ${formatCount(data.summary.exchanges)} trocas`
        }
        actions={
          !sessionless && (
            <Button variant="outline" size="sm" asChild>
              <Link to={sessionTrafficPath(sessionId)}>Ver na página da sessão</Link>
            </Button>
          )
        }
      />
      {!sessionless && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
          <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
            {sessionId}
            <CopyButton value={sessionId} label="Copiar ID da sessão" />
          </span>
          {firstPrompt && (
            <span className="min-w-0 truncate text-muted-foreground" title={firstPrompt}>
              Primeiro prompt: {firstPrompt}
            </span>
          )}
        </div>
      )}

      <TrafficSummary
        summary={data.summary}
        comparison={data.comparison}
        showComparison={!sessionless}
        exchanges={allExchanges}
        allLoaded={!data.truncated}
        runs={data.runs}
      />

      {data.runs.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">{data.runs.length === 1 ? 'Execução de captura:' : 'Execuções de captura:'}</span>
          {data.runs.map((run) => (
            <Link key={run.id} to={runPath(run.id)} className="inline-flex items-center gap-1 underline-offset-4 hover:underline">
              {run.name || run.id}
              <Badge variant="outline" className="text-[10px]" title={RUN_MODE_LABELS[run.mode]}>
                {RUN_STATE_LABELS[run.state] ?? run.state}
              </Badge>
            </Link>
          ))}
        </div>
      )}

      {data.truncated && (
        <Alert>
          <AlertTriangle />
          <AlertDescription>
            A sessão tem mais trocas do que as agrupadas em turnos: as mais recentes ficaram de fora (o resumo acima conta todas).
          </AlertDescription>
        </Alert>
      )}

      <section className="space-y-3" aria-label="Turnos">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-xl font-semibold">Turnos</h2>
          <div className="flex flex-wrap items-center gap-3">
            <div className="flex items-center gap-2">
              <Switch id="show-auxiliary" checked={showAuxiliary} onCheckedChange={setShowAuxiliary} />
              <Label htmlFor="show-auxiliary" className="text-sm font-normal">
                Mostrar chamadas auxiliares
              </Label>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={() => setOpen(new Set(groups.map((t) => t.index)))}>
              Expandir todos
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => setOpen(new Set())}>
              Recolher todos
            </Button>
          </div>
        </div>
        <p className="flex gap-2 text-xs text-muted-foreground">
          <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          {data.note}
        </p>
        {data.turns.length === 0 && !sessionless && (
          <EmptyState
            title="Nenhum turno identificado"
            description="Nenhuma requisição da conversa principal com um prompt digitado foi capturada; as trocas estão abaixo."
            className="py-4"
          />
        )}
        {groups.map((turn) => (
          <TurnCard
            key={turn.index}
            turn={turn}
            open={open.has(turn.index)}
            onOpenChange={(next) => toggle(turn.index, next)}
            showAuxiliary={showAuxiliary || turn.index === 0}
            linkFor={linkFor}
            linkState={linkState}
          />
        ))}
      </section>
    </div>
  )
}
