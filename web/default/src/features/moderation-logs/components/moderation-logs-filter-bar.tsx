import { useCallback, useState } from 'react'
import { useIsFetching, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DataTableToolbar } from '@/components/data-table'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  MODERATION_ACTIONS,
  MODERATION_CATEGORIES,
  MODERATION_SOURCES,
} from '../constants'
import {
  defaultTimeRange,
  filtersToSearch,
  searchToFilters,
} from '../lib/filter'
import type { ModerationLogFilters } from '../types'

const route = getRouteApi('/_authenticated/moderation-logs/')

const ALL = 'all'

interface ModerationLogsFilterBarProps<TData> {
  table: Table<TData>
}

export function ModerationLogsFilterBar<TData>({
  table,
}: ModerationLogsFilterBarProps<TData>) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const searchParams = route.useSearch()
  const fetching = useIsFetching({ queryKey: ['moderation-logs'] })

  const [filters, setFilters] = useState<ModerationLogFilters>(() =>
    searchToFilters(searchParams)
  )

  // URL 是筛选条件的唯一真相：刷新、后退、分享链接都得复现同一份结果。
  // 用渲染期同步而不是 useEffect —— effect 里 setState 会多跑一轮渲染，
  // 表单还会先闪一帧旧值。比较的是序列化后的值，避免 useSearch 换引用时死循环。
  const searchKey = JSON.stringify(searchParams)
  const [syncedKey, setSyncedKey] = useState(searchKey)
  if (syncedKey !== searchKey) {
    setSyncedKey(searchKey)
    setFilters(searchToFilters(searchParams))
  }

  const handleChange = useCallback(
    (field: keyof ModerationLogFilters, value: string | Date | undefined) => {
      setFilters((prev) => ({ ...prev, [field]: value }))
    },
    []
  )

  const handleApply = useCallback(() => {
    navigate({
      to: '/moderation-logs',
      search: { ...filtersToSearch(filters), page: 1 },
    })
    queryClient.invalidateQueries({ queryKey: ['moderation-logs'] })
  }, [filters, navigate, queryClient])

  const handleReset = useCallback(() => {
    const reset = defaultTimeRange()
    setFilters(reset)
    navigate({
      to: '/moderation-logs',
      search: { ...filtersToSearch(reset), page: 1 },
    })
    queryClient.invalidateQueries({ queryKey: ['moderation-logs'] })
  }, [navigate, queryClient])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') handleApply()
    },
    [handleApply]
  )

  const inputClass = 'w-full sm:w-[160px] lg:w-[180px]'

  const hasAdditionalFilters =
    !!filters.word || !!filters.action || !!filters.username
  const hasExpandedFilters =
    !!filters.source ||
    !!filters.category ||
    !!filters.modelName ||
    !!filters.group ||
    !!filters.channelId ||
    !!filters.requestId

  return (
    <DataTableToolbar
      table={table}
      customSearch={
        <CompactDateTimeRangePicker
          start={filters.startTime}
          end={filters.endTime}
          onChange={({ start, end }) => {
            handleChange('startTime', start)
            handleChange('endTime', end)
          }}
          className='w-full sm:w-[340px]'
        />
      }
      additionalSearch={
        <>
          <Input
            placeholder={t('Matched keyword')}
            value={filters.word || ''}
            onChange={(e) => handleChange('word', e.target.value)}
            onKeyDown={handleKeyDown}
            className={inputClass}
          />
          <Select
            items={[
              { value: ALL, label: t('All Decisions') },
              ...MODERATION_ACTIONS.map((a) => ({
                value: a.value,
                label: t(a.label),
              })),
            ]}
            value={filters.action || ALL}
            onValueChange={(value) =>
              handleChange('action', value === ALL ? undefined : (value ?? ''))
            }
          >
            <SelectTrigger className={inputClass}>
              <SelectValue placeholder={t('All Decisions')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value={ALL}>{t('All Decisions')}</SelectItem>
                {MODERATION_ACTIONS.map((a) => (
                  <SelectItem key={a.value} value={a.value}>
                    {t(a.label)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Input
            placeholder={t('Username')}
            value={filters.username || ''}
            onChange={(e) => handleChange('username', e.target.value)}
            onKeyDown={handleKeyDown}
            className={inputClass}
          />
        </>
      }
      expandable={
        <>
          <Select
            items={[
              { value: ALL, label: t('All Risk Categories') },
              ...MODERATION_CATEGORIES.map((c) => ({
                value: c.value,
                label: t(c.label),
              })),
            ]}
            value={filters.category || ALL}
            onValueChange={(value) =>
              handleChange(
                'category',
                value === ALL ? undefined : (value ?? '')
              )
            }
          >
            <SelectTrigger className={inputClass}>
              <SelectValue placeholder={t('All Risk Categories')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value={ALL}>{t('All Risk Categories')}</SelectItem>
                {MODERATION_CATEGORIES.map((c) => (
                  <SelectItem key={c.value} value={c.value}>
                    {t(c.label)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Select
            items={[
              { value: ALL, label: t('All Sources') },
              ...MODERATION_SOURCES.map((s) => ({
                value: s.value,
                label: t(s.label),
              })),
            ]}
            value={filters.source || ALL}
            onValueChange={(value) =>
              handleChange('source', value === ALL ? undefined : (value ?? ''))
            }
          >
            <SelectTrigger className={inputClass}>
              <SelectValue placeholder={t('All Sources')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value={ALL}>{t('All Sources')}</SelectItem>
                {MODERATION_SOURCES.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {t(s.label)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Input
            placeholder={t('Model Name')}
            value={filters.modelName || ''}
            onChange={(e) => handleChange('modelName', e.target.value)}
            onKeyDown={handleKeyDown}
            className={inputClass}
          />
          <Input
            placeholder={t('Group')}
            value={filters.group || ''}
            onChange={(e) => handleChange('group', e.target.value)}
            onKeyDown={handleKeyDown}
            className={inputClass}
          />
          <Input
            placeholder={t('Channel ID')}
            value={filters.channelId || ''}
            onChange={(e) => handleChange('channelId', e.target.value)}
            onKeyDown={handleKeyDown}
            className={inputClass}
          />
          <Input
            placeholder={t('Request ID')}
            value={filters.requestId || ''}
            onChange={(e) => handleChange('requestId', e.target.value)}
            onKeyDown={handleKeyDown}
            className={inputClass}
          />
        </>
      }
      hasAdditionalFilters={hasAdditionalFilters}
      hasExpandedActiveFilters={hasExpandedFilters}
      onSearch={handleApply}
      searchLoading={fetching > 0}
      onReset={handleReset}
    />
  )
}
