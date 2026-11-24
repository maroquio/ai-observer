import { Link } from 'react-router-dom'
import { ArrowRight, Info, Wrench } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { CopyButton } from '@/components/common/copy-button'
import { EmptyState } from '@/components/common/empty-state'
import { formatBytes } from '@/lib/format'
import { TOOL_LINK_STATUS_LABELS, prettyJSON } from '@/lib/httpInspector'
import type { HTTPToolLink } from '@/types/httpInspector'
import { Expandable, TextBlock } from './primitives'

// Shown with every list of tool links, besides the server's note
const NOT_OBSERVED =
  'O proxy observa apenas a rede: vê o modelo pedir a ferramenta (tool_use) e o cliente devolver o resultado (tool_result) no request seguinte. A execução local da ferramenta — comandos, leitura de arquivos, edições — acontece no computador e não é observada diretamente.'

interface ToolLinksProps {
  tools: HTTPToolLink[]
  // The server's reminder that local execution is not observed
  note?: string
  // The inspector path of an exchange
  exchangePathFor: (exchangeId: string) => string
}

/**
 * Tool uses the model requested (tool_use in a reply) and the requests that
 * carried their results (tool_result), with links to both exchanges
 */
export function ToolLinks({ tools, note, exchangePathFor }: ToolLinksProps) {
  return (
    <div className="space-y-4">
      <Alert>
        <Info />
        <AlertTitle>A execução local não é observada pelo proxy</AlertTitle>
        <AlertDescription>
          <p>{NOT_OBSERVED}</p>
          {note && note !== NOT_OBSERVED && <p className="mt-1">{note}</p>}
        </AlertDescription>
      </Alert>
      {tools.length === 0 ? (
        <EmptyState icon={Wrench} title="Nenhum tool_use observado" description="As respostas capturadas não pediram ferramentas." />
      ) : (
        <ul className="space-y-2" aria-label="Ferramentas pedidas e resultados">
          {tools.map((tool) => (
            <ToolLinkItem key={`${tool.toolUseId}:${tool.useExchangeId ?? ''}`} tool={tool} exchangePathFor={exchangePathFor} />
          ))}
        </ul>
      )}
    </div>
  )
}

function ToolLinkItem({ tool, exchangePathFor }: { tool: HTTPToolLink; exchangePathFor: (exchangeId: string) => string }) {
  const statusVariant = tool.status === 'matched' ? 'success' : tool.status === 'pending' ? 'secondary' : 'warning'
  return (
    <li className="space-y-2 rounded-lg border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Wrench className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
        <span className="font-mono text-sm font-medium">{tool.name || 'ferramenta sem nome'}</span>
        <Badge variant={statusVariant} title={tool.evidence}>
          {TOOL_LINK_STATUS_LABELS[tool.status] ?? tool.status}
        </Badge>
        {tool.resultIsError && <Badge variant="destructive">resultado com erro (is_error)</Badge>}
        <span className="ml-auto flex min-w-0 items-center gap-1 font-mono text-xs text-muted-foreground">
          <span className="truncate">{tool.toolUseId}</span>
          <CopyButton value={tool.toolUseId} label="Copiar tool_use.id" />
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-2 text-sm">
        {tool.useExchangeId ? (
          <Link to={exchangePathFor(tool.useExchangeId)} className="underline-offset-4 hover:underline">
            Pedido na troca #{tool.useSeq}
          </Link>
        ) : (
          <span className="text-muted-foreground">Pedido não capturado</span>
        )}
        <ArrowRight className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
        <span className="text-xs text-muted-foreground">execução local (não observada)</span>
        <ArrowRight className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
        {tool.resultExchangeId ? (
          <Link to={exchangePathFor(tool.resultExchangeId)} className="underline-offset-4 hover:underline">
            Resultado enviado na troca #{tool.resultSeq}
          </Link>
        ) : (
          <span className="text-muted-foreground">Resultado ainda não visto</span>
        )}
      </div>
      <p className="text-xs text-muted-foreground">{tool.evidence}</p>

      {(tool.input || tool.resultPreview) && (
        <div className="grid gap-2 lg:grid-cols-2">
          {tool.input && (
            <Expandable title="Entrada (input do tool_use)">
              <TextBlock text={prettyJSON(tool.input).text} label="Entrada da ferramenta" />
            </Expandable>
          )}
          {tool.resultPreview && (
            <Expandable title={`Início do resultado (${formatBytes(tool.resultBytes)} no total)`}>
              <TextBlock text={tool.resultPreview} label="Resultado da ferramenta" />
            </Expandable>
          )}
        </div>
      )}
    </li>
  )
}
