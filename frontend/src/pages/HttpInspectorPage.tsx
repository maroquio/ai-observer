import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, ChevronRight, MessagesSquare, Network } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CopyButton } from '@/components/common/copy-button'
import { ListSkeleton, Refreshing } from '@/components/common/list-skeleton'
import { FilterBar } from '@/components/common/list-filters'
import { PageHeader } from '@/components/common/page-header'
import { QueryError } from '@/components/common/query-error'
import { CaptureHelp } from '@/components/http-inspector/CaptureHelp'
import { InspectorSessionList } from '@/components/http-inspector/InspectorSessionList'
import { Fields } from '@/components/http-inspector/primitives'
import { useLiveInvalidate } from '@/hooks/useLiveInvalidate'
import { useUrlFilters, useUrlWriter } from '@/hooks/useUrlState'
import { errorText } from '@/lib/errors'
import { formatBytes, formatCount } from '@/lib/format'
import { CAPTURE_MODE_LABELS, RUN_MODE_LABELS, RUN_STATE_LABELS, runPath } from '@/lib/httpInspector'
import { queryKeys, useHTTPCaptureRuns, useHTTPInspectorStatus, type HTTPRunFilters } from '@/lib/queries'
import { cn, formatTimestamp } from '@/lib/utils'
import { useTelemetryStore } from '@/stores/telemetryStore'
import type { HTTPCaptureRun, HTTPInspectorStatus } from '@/types/httpInspector'

// URL parameters: the tab (sessions or capture runs) and the run filters
const FILTER_DEFAULTS = { tab: 'sessions', mode: '', state: '' }

/** /http-inspector: the capture proxy's status, the captured sessions (by turns) and the capture runs */
export function HttpInspectorPage() {
  const url = useUrlWriter()
  const [values, setValues] = useUrlFilters(FILTER_DEFAULTS, url)
  const tab = values.tab === 'runs' ? 'runs' : 'sessions'
  const filters = useMemo<HTTPRunFilters>(
    () => ({
      mode: values.mode === 'manual' || values.mode === 'launcher' ? values.mode : undefined,
      state: values.state === 'active' || values.state === 'finished' || values.state === 'interrupted' ? values.state : undefined,
    }),
    [values],
  )
  const status = useHTTPInspectorStatus()
  const runs = useHTTPCaptureRuns(filters, { enabled: tab === 'runs' })
  const rows = useMemo(() => runs.data?.pages.flatMap((p) => p.runs ?? []) ?? [], [runs.data])
  const total = runs.data?.pages[runs.data.pages.length - 1]?.total ?? rows.length

  const updateCount = useTelemetryStore((s) => s.httpExchangesUpdateCount)
  useLiveInvalidate({
    queryKey: [queryKeys.httpInspector.runs, queryKeys.httpInspector.status, queryKeys.httpInspector.inspectorSessions],
    enabled: true,
    intervalMs: 30_000,
    updateCount,
    debounceMs: 1_000,
    maxWaitMs: 5_000,
  })

  let content
  if (runs.isPending) {
    content = <ListSkeleton rows={4} rowClassName="h-20" label="Carregando execuções" />
  } else if (runs.isError && !runs.data) {
    content = <QueryError error={runs.error} title="Não foi possível carregar as execuções" onRetry={() => void runs.refetch()} retrying={runs.isFetching} />
  } else if (rows.length === 0) {
    content =
      filters.mode || filters.state ? (
        <p className="py-6 text-center text-sm text-muted-foreground">Nenhuma execução com estes filtros.</p>
      ) : (
        <CaptureHelp title="Nenhuma execução de captura" intro="Ainda não há tráfego capturado. Inicie o servidor com o proxy e rode o Claude Code pelo launcher:" />
      )
  } else {
    content = (
      <Refreshing active={runs.isFetching && !runs.isFetchingNextPage}>
        <ul className="space-y-2" aria-label="Execuções de captura">
          {rows.map((run) => (
            <RunRow key={run.id} run={run} />
          ))}
        </ul>
        <div className="mt-4 flex flex-col items-center gap-2 text-sm text-muted-foreground">
          <span>
            {formatCount(rows.length)} de {formatCount(total)} execuções
          </span>
          {runs.isFetchNextPageError && <span className="text-error">{errorText(runs.error, 'Falha ao carregar mais execuções')}</span>}
          {runs.hasNextPage && (
            <Button type="button" variant="outline" size="sm" onClick={() => void runs.fetchNextPage()} disabled={runs.isFetchingNextPage}>
              {runs.isFetchingNextPage ? 'Carregando…' : 'Carregar mais execuções'}
            </Button>
          )}
        </div>
      </Refreshing>
    )
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="HTTP Inspector"
        description="As requisições do Claude Code à API da Anthropic, observadas por um proxy local: o que foi enviado, o que voltou e o que aconteceu"
      />

      {status.isPending ? (
        <ListSkeleton rows={1} rowClassName="h-32" label="Carregando status do proxy" />
      ) : status.isError ? (
        <QueryError error={status.error} title="Não foi possível carregar o status do proxy" onRetry={() => void status.refetch()} retrying={status.isFetching} />
      ) : (
        <ProxyStatusCard status={status.data} />
      )}

      <Tabs value={tab} onValueChange={(next) => setValues({ tab: next })}>
        <TabsList aria-label="Visões do HTTP Inspector">
          <TabsTrigger value="sessions">Sessões</TabsTrigger>
          <TabsTrigger value="runs">Execuções de captura</TabsTrigger>
        </TabsList>
        <TabsContent value="sessions" className="mt-4">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <MessagesSquare className="h-5 w-5" aria-hidden="true" />
                Sessões
              </CardTitle>
              <p className="text-sm text-muted-foreground">
                Cada sessão do Claude Code com o tráfego capturado, dividido em turnos: um prompt e as requisições que vieram dele.
              </p>
            </CardHeader>
            <CardContent>
              <InspectorSessionList />
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="runs" className="mt-4">
          <Card>
            <CardHeader className="space-y-4">
              <CardTitle className="flex items-center gap-2">
                <Network className="h-5 w-5" aria-hidden="true" />
                Execuções de captura
              </CardTitle>
              <FilterBar>
                <div className="w-full md:w-64 *:w-full">
                  <NativeSelect aria-label="Modo da execução" className="h-10" value={values.mode} onChange={(e) => setValues({ mode: e.target.value })}>
                    <NativeSelectOption value="">Todos os modos</NativeSelectOption>
                    <NativeSelectOption value="launcher">{RUN_MODE_LABELS.launcher}</NativeSelectOption>
                    <NativeSelectOption value="manual">{RUN_MODE_LABELS.manual}</NativeSelectOption>
                  </NativeSelect>
                </div>
                <div className="w-full md:w-48 *:w-full">
                  <NativeSelect aria-label="Estado da execução" className="h-10" value={values.state} onChange={(e) => setValues({ state: e.target.value })}>
                    <NativeSelectOption value="">Todos os estados</NativeSelectOption>
                    <NativeSelectOption value="active">{RUN_STATE_LABELS.active}</NativeSelectOption>
                    <NativeSelectOption value="finished">{RUN_STATE_LABELS.finished}</NativeSelectOption>
                    <NativeSelectOption value="interrupted">{RUN_STATE_LABELS.interrupted}</NativeSelectOption>
                  </NativeSelect>
                </div>
              </FilterBar>
            </CardHeader>
            <CardContent>{content}</CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}

function ProxyStatusCard({ status }: { status: HTTPInspectorStatus }) {
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0">
        <CardTitle className="text-lg">Proxy de captura</CardTitle>
        <Badge variant={status.enabled ? 'success' : 'outline'}>{status.enabled ? 'Ativo' : 'Desligado'}</Badge>
      </CardHeader>
      <CardContent className="space-y-4">
        {!status.enabled && (
          <p className="text-sm text-muted-foreground">
            O proxy não está rodando neste servidor (ele vem desligado). Execuções já gravadas continuam disponíveis abaixo. Para capturar, inicie com{' '}
            <code className="rounded bg-muted px-1">ai-observer serve --http-proxy</code> e rode{' '}
            <code className="rounded bg-muted px-1">ai-observer run -- claude</code>.
          </p>
        )}
        {status.degraded && (
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Captura degradada desde o início do servidor</AlertTitle>
            <AlertDescription>
              Parte da captura foi perdida (fila cheia ou falha de gravação). O tráfego seguiu normalmente; só o registro ficou incompleto.
            </AlertDescription>
          </Alert>
        )}
        {status.warnings.length > 0 && (
          <ul className="list-disc space-y-1 pl-5 text-sm text-warning" aria-label="Avisos do proxy">
            {status.warnings.map((warning, i) => (
              <li key={i}>{warning}</li>
            ))}
          </ul>
        )}
        <Fields
          label="Configuração do proxy"
          items={[
            {
              label: 'ANTHROPIC_BASE_URL (porta compartilhada)',
              value: status.proxyUrl ? (
                <span className="flex items-center gap-1 font-mono text-xs">
                  {status.proxyUrl}
                  <CopyButton value={status.proxyUrl} label="Copiar URL do proxy" />
                </span>
              ) : (
                <span className="text-muted-foreground">sem porta compartilhada (só execuções do launcher)</span>
              ),
            },
            { label: 'Destino (upstream)', value: <span className="font-mono text-xs">{status.upstream}</span> },
            { label: 'Modo de captura', value: CAPTURE_MODE_LABELS[status.captureMode] ?? status.captureMode },
            ...(status.captureDir ? [{ label: 'Diretório dos corpos', value: <span className="font-mono text-xs break-all">{status.captureDir}</span> }] : []),
            { label: 'Maior corpo gravado', value: formatBytes(status.maxBodyBytes) },
            { label: 'Retenção', value: status.retentionDays > 0 ? `${status.retentionDays} dias` : 'mantém tudo' },
            { label: 'Limite de armazenamento', value: status.storageLimit > 0 ? formatBytes(status.storageLimit) : 'sem limite' },
            { label: 'Execuções ativas', value: formatCount(status.activeRuns) },
          ]}
        />
        {status.enabled && (
          <Fields
            label="Contadores desde o início do servidor"
            items={[
              { label: 'Trocas observadas', value: formatCount(status.exchanges) },
              { label: 'Em andamento', value: formatCount(status.inFlight) },
              { label: 'Bytes encaminhados', value: formatBytes(status.bytesForwarded) },
              { label: 'Bytes gravados', value: formatBytes(status.bytesStored) },
              { label: 'Eventos SSE', value: formatCount(status.sseEvents) },
              {
                label: 'Perdas (registros / bytes / falhas de gravação / erros de parser)',
                value: `${formatCount(status.droppedRecords)} / ${formatBytes(status.lostBodyBytes)} / ${formatCount(status.storeErrors)} / ${formatCount(status.parseErrors)}`,
              },
            ]}
          />
        )}
      </CardContent>
    </Card>
  )
}

function RunRow({ run }: { run: HTTPCaptureRun }) {
  const sessions = run.sessionIds ?? []
  return (
    <li className="relative flex flex-col gap-2 rounded-lg border p-4 transition-colors hover:bg-accent/50 focus-within:ring-2 focus-within:ring-ring/50 sm:flex-row sm:items-center sm:gap-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Link to={runPath(run.id)} className="min-w-0 truncate font-medium outline-none after:absolute after:inset-0 after:content-['']">
            {run.name || run.id}
          </Link>
          <Badge variant={run.state === 'active' ? 'success' : run.state === 'interrupted' ? 'warning' : 'outline'}>
            {RUN_STATE_LABELS[run.state] ?? run.state}
          </Badge>
          <Badge variant="outline">{RUN_MODE_LABELS[run.mode] ?? run.mode}</Badge>
          {run.captureMode !== 'safe' && <Badge variant={run.captureMode === 'full_raw' ? 'destructive' : 'secondary'}>{CAPTURE_MODE_LABELS[run.captureMode]}</Badge>}
        </div>
        {run.command && <p className="truncate font-mono text-xs text-muted-foreground" title={run.command}>{run.command}</p>}
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <span>{formatTimestamp(run.startedAt)}</span>
          <span>
            {formatCount(sessions.length)} {sessions.length === 1 ? 'sessão' : 'sessões'}
          </span>
          {run.unattributed > 0 && <span className={cn('text-warning')}>{formatCount(run.unattributed)} sem sessão</span>}
          <span>{formatBytes(run.storedBytes)} gravados</span>
        </div>
      </div>
      <dl className="flex shrink-0 flex-col-reverse sm:text-right">
        <dt className="text-xs text-muted-foreground">{run.exchanges === 1 ? 'troca' : 'trocas'}</dt>
        <dd className="font-medium tabular-nums">{formatCount(run.exchanges)}</dd>
      </dl>
      <ChevronRight className="hidden h-5 w-5 shrink-0 text-muted-foreground sm:block" aria-hidden="true" />
    </li>
  )
}
