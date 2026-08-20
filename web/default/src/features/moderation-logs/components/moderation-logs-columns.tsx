import { type ColumnDef } from '@tanstack/react-table'
import { Eye, Lock } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatTimestampToDate } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTableColumnHeader } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import {
  MODERATION_ACTION_VARIANTS,
  actionLabel,
  categoryLabel,
  sourceLabel,
} from '../constants'
import type { ModerationLog } from '../types'

/**
 * 后端这几列是分隔符拼接的。空值要滤掉，否则会渲染出空 badge。
 *
 * 两列分隔符不同，别统一：categories 取值是固定枚举，逗号安全；
 * words 是运营自由填写的词条，可能自带逗号，后端用换行拼（model.ModerationWordsSep）。
 * 用错分隔符的表现是把一条规则劈成两个不存在的 badge。
 */
function splitBy(value: string, sep: string): string[] {
  return (value || '')
    .split(sep)
    .map((s) => s.trim())
    .filter(Boolean)
}

export function useModerationLogsColumns(
  onViewContent: (log: ModerationLog) => void
): ColumnDef<ModerationLog>[] {
  const { t } = useTranslation()
  return [
    {
      accessorKey: 'created_at',
      meta: { label: t('Time') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Time')} />
      ),
      cell: ({ row }) => (
        <div className='text-muted-foreground w-[150px] text-xs'>
          {formatTimestampToDate(row.original.created_at)}
        </div>
      ),
    },
    {
      accessorKey: 'action',
      meta: { label: t('Decision'), mobileTitle: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Decision')} />
      ),
      cell: ({ row }) => {
        const action = row.original.action
        // observe 模式下判了 block 但请求实际放行了。不标出来的话，
        // 按 action=block 数出的「拦截量」会把观察期的误杀一并算进去。
        // 读后端的 enforced 列而不是解析 detail JSON：这是持久化字段，
        // 清理任务和统计查询用的也是它，三处口径才一致。
        const observeOnly = action === 'block' && !row.original.enforced
        return (
          <div className='flex items-center gap-1'>
            <StatusBadge
              variant={
                observeOnly
                  ? 'grey'
                  : (MODERATION_ACTION_VARIANTS[action] ?? 'grey')
              }
              label={actionLabel(t, action)}
              size='sm'
            />
            {observeOnly && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Badge variant='outline' className='text-xs'>
                      {t('Observe')}
                    </Badge>
                  }
                />
                <TooltipContent className='max-w-[320px]'>
                  {t(
                    'Observe mode: the verdict was recorded but the request was not actually blocked.'
                  )}
                </TooltipContent>
              </Tooltip>
            )}
          </div>
        )
      },
    },
    {
      accessorKey: 'words',
      meta: { label: t('Matched Keywords') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Matched Keywords')} />
      ),
      cell: ({ row }) => {
        const words = splitBy(row.original.words, '\n')
        if (words.length === 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }
        // 显示的是配置页里那条原文，可以直接拿去「屏蔽词」设置里搜。
        return (
          <div className='flex max-w-[220px] flex-wrap gap-1'>
            {words.map((w) => (
              <Badge key={w} variant='outline' className='font-mono text-xs'>
                {w}
              </Badge>
            ))}
          </div>
        )
      },
      enableSorting: false,
    },
    {
      accessorKey: 'categories',
      meta: { label: t('Category') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Category')} />
      ),
      cell: ({ row }) => {
        const categories = splitBy(row.original.categories, ',')
        if (categories.length === 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }
        return (
          <div className='flex max-w-[180px] flex-wrap gap-1'>
            {categories.map((c) => (
              <Badge key={c} variant='secondary' className='text-xs'>
                {categoryLabel(t, c)}
              </Badge>
            ))}
          </div>
        )
      },
      enableSorting: false,
    },
    {
      accessorKey: 'preview',
      meta: { label: t('Content Preview') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Content Preview')} />
      ),
      cell: ({ row }) => {
        const preview = row.original.preview
        if (!preview) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }
        return (
          <Tooltip>
            <TooltipTrigger
              render={
                <div className='max-w-[280px] truncate text-xs'>{preview}</div>
              }
            />
            <TooltipContent className='max-w-[420px]'>
              <p className='break-all whitespace-pre-wrap'>{preview}</p>
            </TooltipContent>
          </Tooltip>
        )
      },
      enableSorting: false,
    },
    {
      accessorKey: 'username',
      meta: { label: t('User'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('User')} />
      ),
      cell: ({ row }) => (
        <div className='max-w-[120px] truncate text-xs'>
          {row.original.username || `#${row.original.user_id}`}
        </div>
      ),
    },
    {
      accessorKey: 'model_name',
      meta: { label: t('Model'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Model')} />
      ),
      cell: ({ row }) => (
        <div className='max-w-[160px] truncate text-xs'>
          {row.original.model_name || '-'}
        </div>
      ),
    },
    {
      accessorKey: 'source',
      meta: { label: t('Source'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Source')} />
      ),
      cell: ({ row }) => (
        <div className='text-muted-foreground text-xs'>
          {sourceLabel(t, row.original.source)}
          {row.original.provider ? ` · ${row.original.provider}` : ''}
        </div>
      ),
      enableSorting: false,
    },
    {
      id: 'content',
      meta: { label: t('Original Content') },
      header: () => <div className='text-xs'>{t('Original Content')}</div>,
      cell: ({ row }) => {
        // has_content 由后端算：密文的 json tag 是 "-"，前端本来就拿不到它。
        if (!row.original.has_content) {
          return (
            <Tooltip>
              <TooltipTrigger
                render={
                  <span className='text-muted-foreground inline-flex items-center'>
                    <Lock className='size-3.5' />
                  </span>
                }
              />
              <TooltipContent className='max-w-[320px]'>
                {t(
                  'Original content not retained: only blocked records are stored encrypted, and only when the encryption key was configured at write time.'
                )}
              </TooltipContent>
            </Tooltip>
          )
        }
        return (
          <Button
            variant='ghost'
            size='sm'
            className='h-7 px-2'
            onClick={() => onViewContent(row.original)}
          >
            <Eye className='mr-1 size-3.5' />
            {t('View')}
          </Button>
        )
      },
      enableSorting: false,
    },
  ]
}
