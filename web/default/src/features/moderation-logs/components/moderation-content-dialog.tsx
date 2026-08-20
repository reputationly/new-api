import { useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Spinner } from '@/components/ui/spinner'
import { getModerationLogContent } from '../api'
import type { ModerationLog } from '../types'

interface ModerationContentDialogProps {
  log: ModerationLog | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * 查看被拦原文。
 *
 * 内容只在打开弹窗时按需拉取，不随列表预取：每次请求后端都会解密并写一条管理操作审计，
 * 预取会把「管理员看了哪条」这件事变成噪音，审计也就失去意义了。
 */
export function ModerationContentDialog({
  log,
  open,
  onOpenChange,
}: ModerationContentDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  const { data: content = '', isFetching } = useQuery({
    queryKey: ['moderation-log-content', log?.id],
    enabled: open && !!log,
    // 不缓存、不自动重取：每次请求后端都会写一条管理操作审计，
    // 让 react-query 在后台重放会把「谁看了哪条」这件事污染成噪音。
    gcTime: 0,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    queryFn: async () => {
      const res = await getModerationLogContent(log!.id)
      // 失败的 message 由 api 拦截器统一 toast，这里只负责不显示脏数据。
      return res.success ? (res.data?.content ?? '') : ''
    },
  })

  // 关闭时显式清缓存。弹窗常驻挂载，observer 从不归零，gcTime: 0 因此永远不触发；
  // 再叠上 staleTime: Infinity，重开同一条会直接吃缓存——明文照显，审计不写。
  // 「看一次留一次痕」是这个入口唯一的约束，缓存把它绕过去了。
  const handleOpenChange = useCallback(
    (next: boolean) => {
      if (!next && log) {
        queryClient.removeQueries({
          queryKey: ['moderation-log-content', log.id],
        })
      }
      onOpenChange(next)
    },
    [log, onOpenChange, queryClient]
  )

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Original Content')}</DialogTitle>
          <DialogDescription>
            {t('Viewing original content is recorded in the operation log.')}
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className='max-h-[480px] pr-4'>
          <div className='bg-muted/50 relative rounded-md border p-3'>
            {content && (
              <Button
                variant='ghost'
                size='sm'
                className='absolute top-2 right-2 h-8 w-8 p-0'
                onClick={() => copyToClipboard(content)}
                title={t('Copy to clipboard')}
              >
                {copiedText === content ? (
                  <Check className='size-4 text-green-600' />
                ) : (
                  <Copy className='size-4' />
                )}
              </Button>
            )}
            {isFetching ? (
              <div className='text-muted-foreground flex items-center gap-2 text-sm'>
                <Spinner className='size-4' />
                {t('Loading...')}
              </div>
            ) : (
              <p className='pr-10 text-sm leading-relaxed break-all whitespace-pre-wrap'>
                {content || '-'}
              </p>
            )}
          </div>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}
