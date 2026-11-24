import { useCallback, useMemo, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Download, Pencil, Trash2 } from 'lucide-react'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { CopyButton } from '@/components/common/copy-button'
import { QueryError } from '@/components/common/query-error'
import { Fields } from '@/components/http-inspector/primitives'
import { TrafficSummary } from '@/components/http-inspector/TrafficSummary'
import { TrafficViews } from '@/components/http-inspector/TrafficViews'
import { useDebounce } from '@/hooks/useDebounce'
import { useDocumentTitle } from '@/hooks/useDocumentTitle'
import { useDeleteHTTPCaptureRun, useUpdateHTTPCaptureRun } from '@/hooks/useHttpCaptureRunMutations'
import { useLiveInvalidate } from '@/hooks/useLiveInvalidate'
import { useUrlFilters, useUrlWriter } from '@/hooks/useUrlState'
import { httpRunExportUrl } from '@/lib/api'
import { errorText, isHTTPError } from '@/lib/errors'
import { formatBytes, formatCount, formatTimeSpan } from '@/lib/format'
import {
  CAPTURE_MODE_LABELS,
  EXCHANGE_FILTER_DEFAULTS,
  HTTP_INSPECTOR_PATH,
  RUN_MODE_LABELS,
  RUN_STATE_LABELS,
  exchangeFiltersFromValues,
  exchangePath,
  hasExchangeFilters,
  sessionTrafficPath,
} from '@/lib/httpInspector'
import { queryKeys, useHTTPCaptureRun, useHTTPRunExchanges, useHTTPRunTools } from '@/lib/queries'
import { formatTimestamp } from '@/lib/utils'
import { useTelemetryStore } from '@/stores/telemetryStore'
import type { HTTPCaptureRun, HTTPExchange } from '@/types/httpInspector'

const SEARCH_DEBOUNCE_MS = 300
const SECTION_DEFAULTS = { section: 'timeline' }

/** /http-inspector/runs/:runId (the path the CLI prints): a capture run, its exchanges and tools */
export function HttpRunPage() {
  const { runId = '' } = useParams<{ runId: string }>()
  const url = useUrlWriter()
  const [values, setValues] = useUrlFilters(EXCHANGE_FILTER_DEFAULTS, url)
  const [{ section }, setSection] = useUrlFilters(SECTION_DEFAULTS, url)
  const search = useDebounce(values.search, SEARCH_DEBOUNCE_MS)
  const filters = useMemo(() => exchangeFiltersFromValues(values, search), [values, search])

  const detail = useHTTPCaptureRun(runId)
  const exchanges = useHTTPRunExchanges(runId, filters, { enabled: detail.isSuccess })
  const toolsQuery = useHTTPRunTools(runId, { enabled: detail.isSuccess && section === 'tools' })
  const tools = {
    tools: toolsQuery.data?.tools ?? [],
    note: toolsQuery.data?.note ?? '',
    isPending: toolsQuery.isPending,
    isError: toolsQuery.isError,
    error: toolsQuery.error,
    isFetching: toolsQuery.isFetching,
    refetch: toolsQuery.refetch,
  }
  const loaded = useMemo(() => exchanges.data?.pages.flatMap((p) => p.exchanges ?? []) ?? [], [exchanges.data])
  const run = detail.data?.run
  const runModes = useMemo(() => new Map(run ? [[run.id, run.mode] as const] : []), [run])
  const linkFor = useCallback((exchange: HTTPExchange) => exchangePath({ runId }, exchange.id), [runId])
  const exchangePathFor = useCallback((id: string) => exchangePath({ runId }, id), [runId])

  const updateCount = useTelemetryStore((s) => s.httpExchangesUpdateCount)
  useLiveInvalidate({
    queryKey: queryKeys.httpInspector.run(runId),
    enabled: Boolean(runId),
    intervalMs: 30_000,
    updateCount,
    debounceMs: 1_000,
    maxWaitMs: 5_000,
  })

  useDocumentTitle(run ? `${run.name || run.id} · HTTP Inspector` : 'HTTP Inspector')

  const backButton = (
    <Button variant="ghost" size="sm" className="-ml-2 gap-2" asChild>
      <Link to={HTTP_INSPECTOR_PATH}>
        <ArrowLeft className="h-4 w-4" aria-hidden="true" />
        Voltar ao HTTP Inspector
      </Link>
    </Button>
  )

  if (detail.isPending) {
    return (
      <div className="space-y-6">
        {backButton}
        <div className="space-y-3" role="status" aria-busy="true" aria-label="Carregando execução">
          <Skeleton className="h-8 w-1/2" />
          <Skeleton className="h-40 w-full" />
        </div>
      </div>
    )
  }
  if (detail.isError || !detail.data || !run) {
    const notFound = isHTTPError(detail.error, 404)
    return (
      <div className="space-y-6">
        {backButton}
        <QueryError
          error={notFound ? null : detail.error}
          title={notFound ? 'Execução não encontrada' : 'Não foi possível carregar a execução'}
          fallback={notFound ? 'Esta execução de captura não existe ou foi excluída.' : 'Falha ao carregar a execução.'}
          onRetry={notFound ? undefined : () => void detail.refetch()}
          retrying={detail.isFetching}
        />
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {backButton}
      <RunHeader run={run} />
      <TrafficSummary
        summary={detail.data.summary}
        exchanges={loaded}
        allLoaded={!exchanges.hasNextPage && !hasExchangeFilters(values)}
        runs={[run]}
      />
      <Card>
        <CardContent className="pt-6">
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
            showCorrelation
          />
        </CardContent>
      </Card>
    </div>
  )
}

function RunHeader({ run }: { run: HTTPCaptureRun }) {
  const update = useUpdateHTTPCaptureRun(run.id)
  const [editing, setEditing] = useState<'name' | 'note' | null>(null)
  const [draft, setDraft] = useState('')
  const sessions = run.sessionIds ?? []

  const startEdit = (field: 'name' | 'note') => {
    setDraft(field === 'name' ? run.name : (run.note ?? ''))
    setEditing(field)
    update.reset()
  }
  const save = (event: FormEvent) => {
    event.preventDefault()
    if (!editing) return
    const value = draft.trim()
    if (editing === 'name' && !value) return
    update.mutate(editing === 'name' ? { name: value } : { note: value }, { onSuccess: () => setEditing(null) })
  }

  return (
    <Card>
      <CardHeader className="space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1 space-y-2">
            {editing === 'name' ? (
              <EditForm id="run-name" label="Nome da execução" value={draft} onChange={setDraft} onSubmit={save} onCancel={() => setEditing(null)} pending={update.isPending} />
            ) : (
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <h1 className="min-w-0 truncate text-2xl font-bold tracking-tight">{run.name || run.id}</h1>
                <Button type="button" variant="ghost" size="icon-sm" aria-label="Renomear execução" title="Renomear" onClick={() => startEdit('name')}>
                  <Pencil className="h-4 w-4" />
                </Button>
              </div>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={run.state === 'active' ? 'success' : run.state === 'interrupted' ? 'warning' : 'outline'}>
                {RUN_STATE_LABELS[run.state] ?? run.state}
              </Badge>
              <Badge variant="outline">{RUN_MODE_LABELS[run.mode] ?? run.mode}</Badge>
              <Badge variant={run.captureMode === 'full_raw' ? 'destructive' : 'secondary'}>{CAPTURE_MODE_LABELS[run.captureMode] ?? run.captureMode}</Badge>
              <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
                {run.id}
                <CopyButton value={run.id} label="Copiar ID da execução" />
              </span>
            </div>
          </div>
          <RunActions run={run} />
        </div>
        {update.isError && (
          <p className="text-sm text-error" role="alert">
            {errorText(update.error, 'Falha ao salvar')}
          </p>
        )}

        <Fields
          label="Dados da execução"
          items={[
            { label: 'Início', value: formatTimestamp(run.startedAt) },
            { label: 'Fim', value: run.endedAt ? formatTimestamp(run.endedAt) : <span className="text-muted-foreground">em andamento</span> },
            ...(run.endedAt ? [{ label: 'Duração', value: formatTimeSpan(run.startedAt, run.endedAt, 'long') }] : []),
            ...(run.command ? [{ label: 'Comando', value: <span className="font-mono text-xs break-all">{run.command}</span> }] : []),
            ...(run.cwd ? [{ label: 'Diretório', value: <span className="font-mono text-xs break-all">{run.cwd}</span> }] : []),
            ...(run.pid ? [{ label: 'PID', value: String(run.pid) }] : []),
            ...(run.exitCode != null ? [{ label: 'Código de saída', value: String(run.exitCode) }] : []),
            { label: 'Porta do proxy', value: run.port ? String(run.port) : '—' },
            { label: 'Destino (upstream)', value: <span className="font-mono text-xs">{run.upstream}</span> },
            { label: 'Trocas', value: formatCount(run.exchanges) },
            { label: 'Trocas sem sessão', value: formatCount(run.unattributed) },
            { label: 'Corpos gravados', value: formatBytes(run.storedBytes) },
          ]}
        />

        <div className="space-y-1 text-sm">
          <span className="text-muted-foreground">{sessions.length === 1 ? 'Sessão:' : `Sessões (${formatCount(sessions.length)}):`}</span>{' '}
          {sessions.length === 0 ? (
            <span className="text-muted-foreground">nenhuma sessão identificada</span>
          ) : (
            sessions.map((id) => (
              <Link key={id} to={sessionTrafficPath(id)} className="mr-3 font-mono text-xs underline-offset-4 hover:underline">
                {id}
              </Link>
            ))
          )}
        </div>

        {editing === 'note' ? (
          <EditForm id="run-note" label="Nota" value={draft} onChange={setDraft} onSubmit={save} onCancel={() => setEditing(null)} pending={update.isPending} allowEmpty />
        ) : (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted-foreground">Nota:</span>
            {run.note ? <span className="break-words">{run.note}</span> : <span className="text-muted-foreground italic">sem nota</span>}
            <Button type="button" variant="ghost" size="sm" onClick={() => startEdit('note')}>
              {run.note ? 'Editar nota' : 'Adicionar nota'}
            </Button>
          </div>
        )}
      </CardHeader>
    </Card>
  )
}

interface EditFormProps {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  onSubmit: (event: FormEvent) => void
  onCancel: () => void
  pending: boolean
  allowEmpty?: boolean
}

function EditForm({ id, label, value, onChange, onSubmit, onCancel, pending, allowEmpty = false }: EditFormProps) {
  return (
    <form onSubmit={onSubmit} className="flex flex-wrap items-end gap-2">
      <div className="min-w-64 flex-1 space-y-1">
        <Label htmlFor={id}>{label}</Label>
        <Input
          id={id}
          value={value}
          autoFocus
          maxLength={id === 'run-note' ? 4000 : 200}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => e.key === 'Escape' && onCancel()}
        />
      </div>
      <Button type="submit" size="sm" disabled={pending || (!allowEmpty && !value.trim())}>
        {pending ? 'Salvando…' : 'Salvar'}
      </Button>
      <Button type="button" size="sm" variant="ghost" onClick={onCancel} disabled={pending}>
        Cancelar
      </Button>
    </form>
  )
}

function RunActions({ run }: { run: HTTPCaptureRun }) {
  const navigate = useNavigate()
  const remove = useDeleteHTTPCaptureRun()
  const [confirming, setConfirming] = useState(false)
  const active = run.state === 'active'

  const confirmDelete = () => {
    remove.mutate(run.id, {
      onSuccess: () => {
        setConfirming(false)
        navigate(HTTP_INSPECTOR_PATH)
      },
    })
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button variant="outline" size="sm" className="gap-2" asChild>
        {/* Our own API's URL: the zip holds only the redacted bodies */}
        <a href={httpRunExportUrl(run.id)} download title="Exporta registros e corpos redigidos; nunca corpos brutos ou credenciais">
          <Download className="h-4 w-4" aria-hidden="true" />
          Exportar zip (sanitizado)
        </a>
      </Button>
      <Button
        variant="outline"
        size="sm"
        className="gap-2 text-error"
        disabled={active}
        title={active ? 'A execução está ativa: termine-a antes de excluir' : 'Excluir a execução e seus corpos gravados'}
        onClick={() => {
          remove.reset()
          setConfirming(true)
        }}
      >
        <Trash2 className="h-4 w-4" aria-hidden="true" />
        Excluir
      </Button>
      <AlertDialog open={confirming} onOpenChange={(open) => !remove.isPending && setConfirming(open)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Excluir execução de captura</AlertDialogTitle>
            <AlertDialogDescription>
              Excluir “{run.name || run.id}” com suas {formatCount(run.exchanges)} trocas e os corpos gravados? Não é possível desfazer. A telemetria
              OTLP e os arquivos de sessão não são afetados.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {remove.isError && (
            <p className="text-sm text-error" role="alert">
              {isHTTPError(remove.error, 409) ? 'A execução está ativa: termine-a antes de excluir.' : errorText(remove.error, 'Falha ao excluir')}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={remove.isPending}>Cancelar</AlertDialogCancel>
            <Button type="button" variant="destructive" onClick={confirmDelete} disabled={remove.isPending}>
              {remove.isPending ? 'Excluindo…' : 'Excluir'}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
