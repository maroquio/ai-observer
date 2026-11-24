import { describe, it, expect } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToolLinks } from '../ToolLinks'
import { renderWithProviders } from '@/test/queryWrapper'
import { toolsFixture } from '@/test/httpInspectorFixtures'

const pathFor = (id: string) => `/http-inspector/runs/run-1/exchanges/${id}`

describe('ToolLinks', () => {
  it('always says the local execution is not observed, with the server note', () => {
    const { tools, note } = toolsFixture()
    renderWithProviders(<ToolLinks tools={tools} note={note} exchangePathFor={pathFor} />)
    expect(screen.getByText('A execução local não é observada pelo proxy')).toBeInTheDocument()
    expect(screen.getByText(/não é observada diretamente/)).toBeInTheDocument()
    expect(screen.getByText(note)).toBeInTheDocument()
    expect(screen.getAllByText('execução local (não observada)')).toHaveLength(3)
  })

  it('links each tool_use to the exchange that asked for it and the one that returned the result', async () => {
    const user = userEvent.setup()
    const { tools } = toolsFixture()
    renderWithProviders(<ToolLinks tools={tools} exchangePathFor={pathFor} />)
    const [read, bash, edit] = within(screen.getByRole('list', { name: 'Ferramentas pedidas e resultados' })).getAllByRole('listitem')

    expect(within(read).getByText('Read')).toBeInTheDocument()
    expect(within(read).getByText('Resultado encontrado')).toHaveAttribute('title', 'tool_result com o mesmo tool_use_id')
    expect(within(read).getByRole('link', { name: 'Pedido na troca #1' })).toHaveAttribute('href', pathFor('ex-1'))
    expect(within(read).getByRole('link', { name: /troca #6/ })).toHaveAttribute('href', pathFor('ex-6'))

    expect(within(bash).getByText('resultado com erro (is_error)')).toBeInTheDocument()

    expect(within(edit).getByText('Aguardando resultado')).toBeInTheDocument()
    expect(within(edit).queryByRole('link', { name: /Resultado/ })).not.toBeInTheDocument()

    // Input and result are collapsed until asked for, then shown as text
    expect(within(read).queryByLabelText('Entrada da ferramenta')).not.toBeInTheDocument()
    await user.click(within(read).getByRole('button', { name: 'Entrada (input do tool_use)' }))
    expect(within(read).getByLabelText('Entrada da ferramenta')).toHaveTextContent('"file_path": "main.go"')
    await user.click(within(read).getByRole('button', { name: /Início do resultado/ }))
    expect(within(read).getByLabelText('Resultado da ferramenta')).toHaveTextContent('package main')
  })

  it('says when no tool was requested', () => {
    renderWithProviders(<ToolLinks tools={[]} exchangePathFor={pathFor} />)
    expect(screen.getByText('Nenhum tool_use observado')).toBeInTheDocument()
    expect(screen.getByText('A execução local não é observada pelo proxy')).toBeInTheDocument()
  })
})
