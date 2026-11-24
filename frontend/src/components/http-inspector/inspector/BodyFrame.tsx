import type { ReactNode } from 'react'
import { AlertTriangle, Info } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { formatBytes } from '@/lib/format'
import { prettyJSON } from '@/lib/httpInspector'
import type { HTTPExchangeBody, HTTPHeader } from '@/types/httpInspector'
import { RedactedBadge, Section, TextBlock } from '../primitives'

export type BodyMode = 'structured' | 'json'

interface BodyFrameProps {
  // What the body is, e.g. "requisição"
  partLabel: string
  query: {
    data?: HTTPExchangeBody
    isPending: boolean
    isError: boolean
    error: unknown
    isFetching: boolean
    refetch: () => unknown
  }
  mode: BodyMode
  onModeChange: (mode: BodyMode) => void
  variant: 'safe' | 'raw'
  onVariantChange: (variant: 'safe' | 'raw') => void
  // The interpreted view (Estruturado); null when there is none
  structured: (body: HTTPExchangeBody) => ReactNode | null
}

/**
 * One side of an exchange: the toggle between the interpreted view and the
 * body as stored, the safe or raw variant (raw only where full_raw stored
 * one), what was done to the body, and the headers (credentials redacted)
 */
export function BodyFrame({ partLabel, query, mode, onModeChange, variant, onVariantChange, structured }: BodyFrameProps) {
  if (query.isPending) {
    return <ListSkeleton rows={4} label={`Carregando ${partLabel}`} />
  }
  if (query.isError || !query.data) {
    return (
      <QueryError
        error={query.error}
        title={`Não foi possível carregar a ${partLabel}`}
        onRetry={() => void query.refetch()}
        retrying={query.isFetching}
      />
    )
  }
  const body = query.data
  const rawAvailable = body.available.includes('raw')
  const structuredView = structured(body)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <div role="group" aria-label="Forma de exibição" className="inline-flex rounded-md border p-0.5">
          <Button type="button" size="sm" variant={mode === 'structured' ? 'secondary' : 'ghost'} aria-pressed={mode === 'structured'} onClick={() => onModeChange('structured')}>
            Estruturado
          </Button>
          <Button type="button" size="sm" variant={mode === 'json' ? 'secondary' : 'ghost'} aria-pressed={mode === 'json'} onClick={() => onModeChange('json')}>
            JSON original
          </Button>
        </div>
        {rawAvailable && (
          <div role="group" aria-label="Variante do corpo" className="inline-flex rounded-md border p-0.5">
            <Button type="button" size="sm" variant={variant === 'safe' ? 'secondary' : 'ghost'} aria-pressed={variant === 'safe'} onClick={() => onVariantChange('safe')}>
              Seguro (redigido)
            </Button>
            <Button type="button" size="sm" variant={variant === 'raw' ? 'secondary' : 'ghost'} aria-pressed={variant === 'raw'} onClick={() => onVariantChange('raw')}>
              Bruto (full_raw)
            </Button>
          </div>
        )}
        <BodyBadges body={body} />
      </div>

      {variant === 'raw' && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Variante bruta: dados sensíveis</AlertTitle>
          <AlertDescription>Este é o corpo como passou pela rede, sem mascarar segredos. Ele fica só neste computador e nunca é exportado.</AlertDescription>
        </Alert>
      )}

      {body.unavailable && (
        <Alert>
          <Info />
          <AlertTitle>Corpo indisponível</AlertTitle>
          <AlertDescription>{body.unavailable}</AlertDescription>
        </Alert>
      )}

      {mode === 'structured' ? (
        structuredView ?? <RawBody body={body} partLabel={partLabel} />
      ) : (
        <RawBody body={body} partLabel={partLabel} />
      )}

      <Section title="Cabeçalhos">
        <HeadersTable headers={body.headers} />
      </Section>
    </div>
  )
}

function BodyBadges({ body }: { body: HTTPExchangeBody }) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      {body.sanitized && <RedactedBadge count={body.redactions} />}
      {body.truncated && (
        <Badge variant="warning" title="O corpo passou do limite de armazenamento; só o início foi gravado (o tráfego seguiu inteiro)">
          truncado
        </Badge>
      )}
      {body.contentType && <span className="font-mono">{body.contentType}</span>}
      {body.contentEncoding && <span className="font-mono">({body.contentEncoding})</span>}
      <span>
        {formatBytes(body.observedBytes)} na rede{body.storedBytes > 0 && body.storedBytes !== body.observedBytes ? `, ${formatBytes(body.storedBytes)} decodificados` : ''}
      </span>
      {body.decodeStatus && body.decodeStatus !== 'identity' && body.decodeStatus !== 'decoded' && (
        <Badge variant="warning">decodificação: {body.decodeStatus}</Badge>
      )}
    </div>
  )
}

/** The body as stored, JSON indented; base64 bodies are binary */
function RawBody({ body, partLabel }: { body: HTTPExchangeBody; partLabel: string }) {
  if (body.body === undefined || body.body === '') {
    return body.unavailable ? null : <p className="text-sm text-muted-foreground">Sem corpo.</p>
  }
  if (body.bodyEncoding === 'base64') {
    return (
      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">Corpo binário (não é UTF-8 válido), mostrado em base64.</p>
        <TextBlock text={body.body} label={`Corpo da ${partLabel} (base64)`} />
      </div>
    )
  }
  return <TextBlock text={prettyJSON(body.body).text} label={`Corpo da ${partLabel}`} />
}

function HeadersTable({ headers }: { headers: HTTPHeader[] }) {
  if (headers.length === 0) {
    return <p className="text-sm text-muted-foreground">Nenhum cabeçalho gravado.</p>
  }
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table aria-label="Cabeçalhos">
        <TableHeader>
          <TableRow>
            <TableHead>Nome</TableHead>
            <TableHead>Valor</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {headers.map((header, i) => (
            <TableRow key={`${header.name}:${i}`}>
              <TableCell className="font-mono text-xs align-top">{header.name}</TableCell>
              <TableCell className="font-mono text-xs break-all whitespace-normal">
                {header.value}
                {header.redacted && (
                  <Badge variant="warning" className="ml-2" title="Credencial ou cookie: o valor nunca é gravado">
                    mascarado
                  </Badge>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
