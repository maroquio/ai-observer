import { useState, type ReactNode } from 'react'
import { useWindowVirtualizer } from '@tanstack/react-virtual'
import { cn } from '@/lib/utils'

interface VirtualRowsProps {
  count: number
  // Every row has this height (px): rows are not measured
  rowHeight: number
  renderRow: (index: number) => ReactNode
  // A stable key of each row (e.g. the exchange ID)
  rowKey?: (index: number) => string | number
  overscan?: number
  className?: string
  // ARIA role of the rows' container (rowgroup inside a table)
  role?: string
}

/**
 * Rows of equal height virtualized in the window's scroll (as the
 * transcript list): only those near the viewport are rendered, so lists of
 * thousands of exchanges or stream events stay fast.
 */
export function VirtualRows({ count, rowHeight, renderRow, rowKey, overscan = 10, className, role }: VirtualRowsProps) {
  // The distance from the top of the document, read on each render as the
  // content above the list changes height
  const [node, setNode] = useState<HTMLDivElement | null>(null)
  const scrollMargin = node ? node.getBoundingClientRect().top + window.scrollY : 0

  const virtualizer = useWindowVirtualizer({
    count,
    estimateSize: () => rowHeight,
    overscan,
    scrollMargin,
    getItemKey: rowKey,
  })
  const items = virtualizer.getVirtualItems()

  return (
    <div ref={setNode} role={role} className={cn('relative w-full', className)} style={{ height: `${virtualizer.getTotalSize()}px` }}>
      {items.map((item) => (
        <div
          key={item.key}
          role="none"
          data-index={item.index}
          className="absolute left-0 top-0 w-full"
          style={{ height: `${rowHeight}px`, transform: `translateY(${item.start - virtualizer.options.scrollMargin}px)` }}
        >
          {renderRow(item.index)}
        </div>
      ))}
    </div>
  )
}
