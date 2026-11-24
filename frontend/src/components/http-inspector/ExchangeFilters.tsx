import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { FilterBar, SearchFilter } from '@/components/common/list-filters'
import { KIND_LABELS, STATE_LABELS, hasExchangeFilters, type ExchangeFilterValues } from '@/lib/httpInspector'

interface ExchangeFiltersProps {
  values: ExchangeFilterValues
  onChange: (updates: Partial<ExchangeFilterValues>) => void
}

/** Filters of an exchange timeline: kind, HTTP status, state and text */
export function ExchangeFilters({ values, onChange }: ExchangeFiltersProps) {
  return (
    <FilterBar>
      <div className="w-full md:w-48 *:w-full">
        <NativeSelect aria-label="Tipo de troca" className="h-10" value={values.kind} onChange={(e) => onChange({ kind: e.target.value })}>
          <NativeSelectOption value="">Todos os tipos</NativeSelectOption>
          {Object.entries(KIND_LABELS).map(([value, label]) => (
            <NativeSelectOption key={value} value={value}>
              {label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </div>
      <div className="w-full md:w-44 *:w-full">
        <NativeSelect aria-label="Status HTTP" className="h-10" value={values.status} onChange={(e) => onChange({ status: e.target.value })}>
          <NativeSelectOption value="">Qualquer status</NativeSelectOption>
          <NativeSelectOption value="ok">Sucesso (2xx/3xx)</NativeSelectOption>
          <NativeSelectOption value="error">Erros (4xx/5xx ou sem resposta)</NativeSelectOption>
        </NativeSelect>
      </div>
      <div className="w-full md:w-48 *:w-full">
        <NativeSelect aria-label="Estado da troca" className="h-10" value={values.state} onChange={(e) => onChange({ state: e.target.value })}>
          <NativeSelectOption value="">Qualquer estado</NativeSelectOption>
          {Object.entries(STATE_LABELS).map(([value, label]) => (
            <NativeSelectOption key={value} value={value}>
              {label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </div>
      <SearchFilter
        label="Buscar trocas"
        placeholder="Buscar por caminho, descrição, modelo, sessão ou request-id"
        value={values.search}
        onChange={(search) => onChange({ search })}
      />
      {hasExchangeFilters(values) && (
        <Button type="button" variant="ghost" size="sm" onClick={() => onChange({ kind: '', status: '', state: '', search: '' })}>
          Limpar filtros
        </Button>
      )}
    </FilterBar>
  )
}
