import { useCallback, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import {
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { useMediaQuery } from '@/hooks'
import { useTranslation } from 'react-i18next'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { DataTablePage } from '@/components/data-table'
import { getModerationLogs } from '../api'
import { searchToParams } from '../lib/filter'
import type { ModerationLog } from '../types'
import { ModerationContentDialog } from './moderation-content-dialog'
import { useModerationLogsColumns } from './moderation-logs-columns'
import { ModerationLogsFilterBar } from './moderation-logs-filter-bar'

const route = getRouteApi('/_authenticated/moderation-logs/')

export function ModerationLogsTable() {
  const { t } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const searchParams = route.useSearch()

  const [viewing, setViewing] = useState<ModerationLog | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)

  const handleViewContent = useCallback((log: ModerationLog) => {
    setViewing(log)
    setDialogOpen(true)
  }, [])

  const columns = useModerationLogsColumns(handleViewContent)

  const { pagination, onPaginationChange, ensurePageInRange } =
    useTableUrlState({
      search: searchParams,
      navigate: route.useNavigate(),
      pagination: { defaultPage: 1, defaultPageSize: isMobile ? 20 : 50 },
      globalFilter: { enabled: false },
      columnFilters: [],
    })

  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'moderation-logs',
      pagination.pageIndex + 1,
      pagination.pageSize,
      searchParams,
    ],
    queryFn: async () => {
      const res = await getModerationLogs(
        searchToParams(
          searchParams,
          pagination.pageIndex + 1,
          pagination.pageSize
        )
      )
      // 失败的 message 由 api 拦截器统一 toast，这里给空数据即可。
      return {
        items: res.data?.items || [],
        total: res.data?.total || 0,
      }
    },
    placeholderData: (previousData) => previousData,
  })

  const table = useReactTable({
    data: data?.items || [],
    columns,
    state: { pagination },
    enableRowSelection: false,
    onPaginationChange,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    manualPagination: true,
    pageCount: Math.ceil((data?.total || 0) / pagination.pageSize),
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No Moderation Logs Found')}
        emptyDescription={t(
          'No moderation logs in the selected time range. Records appear here once content is moderated.'
        )}
        skeletonKeyPrefix='moderation-logs-skeleton'
        tableClassName='max-h-[calc(100dvh-13rem)] overflow-auto sm:max-h-[calc(100dvh-14rem)]'
        tableHeaderClassName='bg-muted/30 sticky top-0 z-10'
        toolbar={<ModerationLogsFilterBar table={table} />}
      />

      <ModerationContentDialog
        log={viewing}
        open={dialogOpen}
        onOpenChange={setDialogOpen}
      />
    </>
  )
}
