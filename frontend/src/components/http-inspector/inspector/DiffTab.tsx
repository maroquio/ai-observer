import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, Info } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { formatBytes, formatCount } from '@/lib/format'
import { comparedByLabel, exchangePath, type ExchangeContext } from '@/lib/httpInspector'
import { useHTTPExchangeDiff } from '@/lib/queries'
import type { WireCacheBreakpoint, WireRequestDiff } from '@/types/httpInspector'
import { Expandable, Fields, JsonBlock, OriginBadge, Section } from '../primitives'

/**
 * Context Diff: what this request changed against the previous one of its
 * conversation (fingerprints of the bodies, never the bodies themselves)
 */
export function DiffTab({ exchangeId, context }: { exchangeId: string; context: ExchangeContext }) {
  const query = useHTTPExchangeDiff(exchangeId)
  if (query.isPending) {
    return <ListSkeleton rows={5} label="Carregando comparação" />
  }
  if (query.isError) {
    return <QueryError error={query.error} title="Não foi possível comparar a requisição" onRetry={() => void query.refetch()} retrying={query.isFetching} />
  }
  const { diff, previousId, previousSeq, comparedBy } = query.data

  if (!previousId) {
    return (
      <div className="space-y-6">
        <Alert>
          <Info />
          <AlertTitle>Primeira requisição: nada para comparar</AlertTitle>
          <AlertDescription>
            Não há requisição anterior desta conversa (mesma sessão, execução e tipo) capturada antes desta. Tudo o que ela leva é novo para o
            proxy: system, ferramentas e {formatCount(diff.messagesAfter)} {diff.messagesAfter === 1 ? 'mensagem' : 'mensagens'}.
          </AlertDescription>
        </Alert>
        <Fields
          label="O que esta requisição leva"
          items={[
            { label: 'Mensagens', value: formatCount(diff.messagesAfter) },
            { label: 'Bytes das mensagens', value: formatBytes(diff.newBytes + diff.repeatedBytes) },
            { label: 'Ferramentas', value: formatCount(diff.toolsCountAfter) },
            { label: 'System', value: formatBytes(diff.systemBytesAfter) },
            { label: 'Pontos de cache', value: formatCount((diff.cacheBreakpointsAfter ?? []).length) },
          ]}
        />
        <Breakpoints title="Pontos de cache (cache_control)" breakpoints={diff.cacheBreakpointsAfter} />
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <p className="flex flex-wrap items-center gap-2 text-sm">
        Comparada com a{' '}
        <Link to={exchangePath(context, previousId)} className="font-medium underline-offset-4 hover:underline">
          troca #{previousSeq}
        </Link>
        <span className="text-muted-foreground">(critério: {comparedByLabel(comparedBy)})</span>
        <OriginBadge origin="derivado" />
      </p>

      {diff.possibleCompaction && (
        <Alert>
          <AlertTriangle />
          <AlertTitle>Possível compactação do histórico</AlertTitle>
          <AlertDescription>
            O histórico foi reescrito: o prefixo comum ficou menor que a requisição anterior e o número de mensagens caiu. É um indício, não uma
            certeza.
          </AlertDescription>
        </Alert>
      )}

      <Section title="Mensagens">
        <Fields
          items={[
            { label: 'Antes → depois', value: `${formatCount(diff.messagesBefore)} → ${formatCount(diff.messagesAfter)}` },
            { label: 'Prefixo comum (mensagens iguais)', value: formatCount(diff.commonPrefix) },
            { label: 'Mensagens adicionadas', value: <Changed n={diff.messagesAdded} /> },
            { label: 'Mensagens substituídas', value: <Changed n={diff.messagesReplaced} warn /> },
            { label: 'Bytes novos', value: formatBytes(diff.newBytes) },
            { label: 'Bytes reenviados (histórico)', value: formatBytes(diff.repeatedBytes), hint: 'O histórico vai inteiro em cada requisição' },
          ]}
        />
      </Section>

      <Section title="Novos tool_result">
        {(diff.newToolResults ?? []).length === 0 ? (
          <p className="text-sm text-muted-foreground">Nenhum tool_result novo.</p>
        ) : (
          <ul className="flex flex-wrap gap-2">
            {(diff.newToolResults ?? []).map((id) => (
              <li key={id}>
                <Badge variant="outline" className="font-mono">
                  {id}
                </Badge>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section title="Parâmetros e modelo">
        <Fields
          items={[
            {
              label: 'Modelo',
              value: diff.modelChanged ? (
                <span>
                  {diff.modelBefore || '—'} → {diff.modelAfter || '—'} <Badge variant="warning">mudou</Badge>
                </span>
              ) : (
                diff.modelAfter || '—'
              ),
            },
          ]}
        />
        {(diff.paramChanges ?? []).length === 0 ? (
          <p className="text-sm text-muted-foreground">Nenhum parâmetro mudou.</p>
        ) : (
          <div className="overflow-x-auto rounded-md border">
            <Table aria-label="Parâmetros alterados">
              <TableHeader>
                <TableRow>
                  <TableHead>Campo</TableHead>
                  <TableHead>Antes</TableHead>
                  <TableHead>Depois</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(diff.paramChanges ?? []).map((change) => (
                  <TableRow key={change.field}>
                    <TableCell className="font-mono text-xs">{change.field}</TableCell>
                    <TableCell className="font-mono text-xs break-all whitespace-normal">{change.before || 'ausente'}</TableCell>
                    <TableCell className="font-mono text-xs break-all whitespace-normal">{change.after || 'ausente'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Section>

      <Section title="System e ferramentas">
        <Fields
          items={[
            {
              label: 'System',
              value: diff.systemChanged ? <Badge variant="warning">mudou</Badge> : 'igual',
              hint: `${formatBytes(diff.systemBytesBefore)} → ${formatBytes(diff.systemBytesAfter)}${diff.systemSha256After ? ` · sha256 ${diff.systemSha256After.slice(0, 12)}…` : ''}`,
            },
            {
              label: 'Ferramentas',
              value: diff.toolsChanged ? <Badge variant="warning">mudaram</Badge> : 'iguais',
              hint: `${formatCount(diff.toolsCountBefore)} → ${formatCount(diff.toolsCountAfter)} · ${formatBytes(diff.toolsBytesBefore)} → ${formatBytes(diff.toolsBytesAfter)}`,
            },
          ]}
        />
        <NameList label="Adicionadas" names={diff.toolsAdded} />
        <NameList label="Removidas" names={diff.toolsRemoved} />
        <NameList label="Alteradas" names={diff.toolsModified} />
      </Section>

      <Section title="Pontos de cache" actions={diff.cacheChanged ? <Badge variant="warning">mudaram</Badge> : <span className="text-xs text-muted-foreground">iguais</span>}>
        <div className="grid gap-4 md:grid-cols-2">
          <Breakpoints title="Antes" breakpoints={diff.cacheBreakpointsBefore} />
          <Breakpoints title="Depois" breakpoints={diff.cacheBreakpointsAfter} />
        </div>
      </Section>

      <Expandable title="Comparação completa (JSON)">
        <JsonBlock value={diff satisfies WireRequestDiff} label="Context Diff" />
      </Expandable>
    </div>
  )
}

function Changed({ n, warn = false }: { n: number; warn?: boolean }) {
  return <span className={n > 0 ? (warn ? 'text-warning' : 'text-success') : undefined}>{formatCount(n)}</span>
}

function NameList({ label, names }: { label: string; names: string[] | null }): ReactNode {
  if (!names || names.length === 0) return null
  return (
    <p className="text-sm">
      <span className="text-muted-foreground">{label}: </span>
      <span className="font-mono text-xs">{names.join(', ')}</span>
    </p>
  )
}

function Breakpoints({ title, breakpoints }: { title: string; breakpoints: WireCacheBreakpoint[] | null }) {
  const list = breakpoints ?? []
  return (
    <div className="space-y-1">
      <h4 className="text-sm font-medium">{title}</h4>
      {list.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nenhum.</p>
      ) : (
        <ul className="space-y-0.5">
          {list.map((bp, i) => (
            <li key={`${bp.location}:${i}`} className="font-mono text-xs">
              {bp.location} {bp.ttl ? `ttl=${bp.ttl}` : ''}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
