import { useEffect } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

/**
 * 内容审核设置。见 docs/content-moderation-design.md §8。
 *
 * 放在「安全」分区里屏蔽词的旁边：运营调词库和调审核模式本来就是同一件事，
 * 拆成两个入口只会让人以为它们互不相干（实际上关键词层同时受两边开关约束）。
 *
 * 未纳入本页：policies / group_policies / endpoints。它们服务于 L1 及以上的
 * 远程分类器，第一期没有任何消费方，做出来的表单只会是个没人填的空壳。
 */
const moderationSchema = z.object({
  'moderation.mode': z.enum(['off', 'observe', 'blocking']),
  'moderation.keyword_enabled': z.boolean(),
  // 0 = 完全不记通过请求，1 = 全量记。范围校验放在这里，
  // 后端只做 <=0 / >=1 的截断，不会拒绝非法值。
  'moderation.log_pass_sample_rate': z.number().min(0).max(1),
  'moderation.log_queue_size': z.number().int().min(1),
  'moderation.retention_block_days': z.number().int().min(1),
  'moderation.retention_pass_days': z.number().int().min(1),
  filterMode: z.enum(['all', 'include', 'exclude']),
  filterModels: z.string(),
})

type ModerationFormValues = z.infer<typeof moderationSchema>

/** model_filter 是嵌套结构，配置管理器把它整体存成一个 JSON 键。 */
type ModelFilter = { mode?: string; models?: string[] }

function parseModelFilter(raw: string): ModelFilter {
  if (!raw) return { mode: 'all', models: [] }
  try {
    return JSON.parse(raw) as ModelFilter
  } catch {
    // 解析不出来就按「全部」处理。这个字段决定哪些模型走审核，
    // 坏值当成 include 会静默漏审，当成 all 最多是多审几个模型。
    return { mode: 'all', models: [] }
  }
}

type ModerationSectionProps = {
  defaultValues: {
    'moderation.mode': string
    'moderation.keyword_enabled': boolean
    'moderation.log_pass_sample_rate': number
    'moderation.log_queue_size': number
    'moderation.retention_block_days': number
    'moderation.retention_pass_days': number
    'moderation.model_filter': string
  }
}

export function ModerationSection({ defaultValues }: ModerationSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const filter = parseModelFilter(defaultValues['moderation.model_filter'])
  const formDefaults: ModerationFormValues = {
    'moderation.mode': (defaultValues['moderation.mode'] ||
      'off') as ModerationFormValues['moderation.mode'],
    'moderation.keyword_enabled': defaultValues['moderation.keyword_enabled'],
    'moderation.log_pass_sample_rate':
      defaultValues['moderation.log_pass_sample_rate'],
    'moderation.log_queue_size': defaultValues['moderation.log_queue_size'],
    'moderation.retention_block_days':
      defaultValues['moderation.retention_block_days'],
    'moderation.retention_pass_days':
      defaultValues['moderation.retention_pass_days'],
    filterMode: (filter.mode || 'all') as ModerationFormValues['filterMode'],
    filterModels: (filter.models || []).join('\n'),
  }

  const form = useForm<ModerationFormValues>({
    resolver: zodResolver(moderationSchema),
    defaultValues: formDefaults,
  })

  useEffect(() => {
    form.reset(formDefaults)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defaultValues])

  // 运行态：原文能不能留存取决于一个只在环境变量里的密钥，
  // 不在这里显示的话，「为什么看不到原文」只能靠翻服务日志回答。
  const { data: status } = useQuery({
    queryKey: ['moderation-status'],
    queryFn: async () => {
      const res = await api.get('/api/moderation/status')
      return res.data?.data as
        | {
            encrypt_key_ready: boolean
            encrypt_key_misconfigured: boolean
            dropped_logs: number
          }
        | undefined
    },
  })

  const mode = form.watch('moderation.mode')
  const filterMode = form.watch('filterMode')

  const onSubmit = async (values: ModerationFormValues) => {
    const nextFilter = JSON.stringify({
      mode: values.filterMode,
      models: values.filterModels
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
    })

    const updates: [string, string | number | boolean][] = []
    for (const key of [
      'moderation.mode',
      'moderation.keyword_enabled',
      'moderation.log_pass_sample_rate',
      'moderation.log_queue_size',
      'moderation.retention_block_days',
      'moderation.retention_pass_days',
    ] as const) {
      if (values[key] !== formDefaults[key]) updates.push([key, values[key]])
    }
    if (
      nextFilter !==
      JSON.stringify({
        mode: formDefaults.filterMode,
        models: formDefaults.filterModels
          .split('\n')
          .map((s) => s.trim())
          .filter(Boolean),
      })
    ) {
      updates.push(['moderation.model_filter', nextFilter])
    }

    for (const [key, value] of updates) {
      await updateOption.mutateAsync({ key, value })
    }
  }

  return (
    <SettingsSection
      title={t('Content Moderation')}
      description={t(
        'Decide whether prompts are moderated, and how moderation records are kept.'
      )}
    >
      {status?.encrypt_key_misconfigured && (
        <Alert variant='destructive' className='mb-4'>
          <AlertDescription>
            {t(
              'MODERATION_ENCRYPT_KEY is set but invalid (it must be 64 hex characters). Blocked content is not being retained.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {status &&
        !status.encrypt_key_ready &&
        !status.encrypt_key_misconfigured && (
          <Alert className='mb-4'>
            <AlertDescription>
              {t(
                'MODERATION_ENCRYPT_KEY is not configured. Moderation still runs, but blocked content is not retained and cannot be reviewed later.'
              )}
            </AlertDescription>
          </Alert>
        )}
      {!!status?.dropped_logs && (
        <Alert variant='destructive' className='mb-4'>
          <AlertDescription>
            {t('Moderation log queue overflowed; records were dropped:')}{' '}
            {status.dropped_logs}
          </AlertDescription>
        </Alert>
      )}

      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <FormField
            control={form.control}
            name='moderation.mode'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Moderation mode')}</FormLabel>
                <Select
                  items={[
                    { value: 'off', label: t('Off') },
                    { value: 'observe', label: t('Observe only') },
                    { value: 'blocking', label: t('Block') },
                  ]}
                  value={field.value}
                  onValueChange={(v) => field.onChange(v ?? 'off')}
                >
                  <FormControl>
                    <SelectTrigger className='w-full sm:w-[280px]'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='off'>{t('Off')}</SelectItem>
                      <SelectItem value='observe'>
                        {t('Observe only')}
                      </SelectItem>
                      <SelectItem value='blocking'>{t('Block')}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Off: keyword blocking behaves as before. Observe: verdicts are recorded but nothing is blocked. Block: the full chain rejects violations.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {mode === 'observe' && (
            <Alert>
              <AlertDescription>
                {t(
                  'In observe mode keyword hits are recorded but NOT blocked, and only a 160-character preview is retained instead of the full content.'
                )}
              </AlertDescription>
            </Alert>
          )}

          <FormField
            control={form.control}
            name='moderation.keyword_enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
                  <FormLabel className='text-base'>
                    {t('Keyword layer (L0)')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Also requires the sensitive word switches above; either one turns it off.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='filterMode'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Model scope')}</FormLabel>
                <Select
                  items={[
                    { value: 'all', label: t('All models') },
                    { value: 'include', label: t('Only listed models') },
                    { value: 'exclude', label: t('Exclude listed models') },
                  ]}
                  value={field.value}
                  onValueChange={(v) => field.onChange(v ?? 'all')}
                >
                  <FormControl>
                    <SelectTrigger className='w-full sm:w-[280px]'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='all'>{t('All models')}</SelectItem>
                      <SelectItem value='include'>
                        {t('Only listed models')}
                      </SelectItem>
                      <SelectItem value='exclude'>
                        {t('Exclude listed models')}
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Models outside the scope fall back to keyword-only checking, they are not left unmoderated.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {filterMode !== 'all' && (
            <FormField
              control={form.control}
              name='filterModels'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Model patterns')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={5}
                      placeholder={'text-embedding-*\ngpt-4o-mini'}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'One pattern per line. Only a trailing * is supported; regular expressions are not, because a wrong one fails silently instead of erroring.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='moderation.log_pass_sample_rate'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Pass log sample rate')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='0.01'
                      min='0'
                      max='1'
                      {...field}
                      onChange={(e) => field.onChange(Number(e.target.value))}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Fraction of clean requests recorded (0-1). Blocked records are always kept in full. No pass records are written while the mode is Off.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='moderation.log_queue_size'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Log queue size')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='1'
                      {...field}
                      onChange={(e) => field.onChange(Number(e.target.value))}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Records are written asynchronously. When the queue is full they are dropped rather than delaying the request. Takes effect after a restart.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='moderation.retention_block_days'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Blocked record retention (days)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='1'
                      {...field}
                      onChange={(e) => field.onChange(Number(e.target.value))}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Cleanup is a hard delete with no second copy, so only increase this value unless you are certain.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='moderation.retention_pass_days'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Pass record retention (days)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='1'
                      {...field}
                      onChange={(e) => field.onChange(Number(e.target.value))}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Pass records carry no content, only a hash and metadata.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending
              ? t('Saving...')
              : t('Save moderation settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}
