import { useRef, useState } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import i18next from 'i18next'
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
import { Switch } from '@/components/ui/switch'
import { testNotification } from '../api'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'

const optionalUrl = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  return /^https?:\/\/.+/.test(trimmed)
}, 'Enter a valid URL starting with http(s):// or leave blank')

const notificationSchema = z.object({
  notification_setting: z.object({
    wechat_work_webhook_url: optionalUrl,
    dingtalk_webhook_url: optionalUrl,
    notify_feedback: z.boolean(),
    notify_enterprise: z.boolean(),
    notify_kyc: z.boolean(),
    notify_bank_transfer: z.boolean(),
    notify_invoice: z.boolean(),
  }),
})

type NotificationFormValues = z.infer<typeof notificationSchema>

const DOT_KEYS = [
  'notification_setting.wechat_work_webhook_url',
  'notification_setting.dingtalk_webhook_url',
  'notification_setting.notify_feedback',
  'notification_setting.notify_enterprise',
  'notification_setting.notify_kyc',
  'notification_setting.notify_bank_transfer',
  'notification_setting.notify_invoice',
] as const

type DotKey = (typeof DOT_KEYS)[number]

type AdminNotificationSectionProps = {
  defaultValues: Record<DotKey, string | boolean>
}

const buildDefaults = (
  defaults: AdminNotificationSectionProps['defaultValues']
): NotificationFormValues => ({
  notification_setting: {
    wechat_work_webhook_url: String(
      defaults['notification_setting.wechat_work_webhook_url'] ?? ''
    ),
    dingtalk_webhook_url: String(
      defaults['notification_setting.dingtalk_webhook_url'] ?? ''
    ),
    notify_feedback: Boolean(defaults['notification_setting.notify_feedback']),
    notify_enterprise: Boolean(
      defaults['notification_setting.notify_enterprise']
    ),
    notify_kyc: Boolean(defaults['notification_setting.notify_kyc']),
    notify_bank_transfer: Boolean(
      defaults['notification_setting.notify_bank_transfer']
    ),
    notify_invoice: Boolean(defaults['notification_setting.notify_invoice']),
  },
})

const flatten = (
  values: NotificationFormValues
): Record<DotKey, string | boolean> => {
  const s = values.notification_setting
  return {
    'notification_setting.wechat_work_webhook_url':
      s.wechat_work_webhook_url.trim(),
    'notification_setting.dingtalk_webhook_url': s.dingtalk_webhook_url.trim(),
    'notification_setting.notify_feedback': s.notify_feedback,
    'notification_setting.notify_enterprise': s.notify_enterprise,
    'notification_setting.notify_kyc': s.notify_kyc,
    'notification_setting.notify_bank_transfer': s.notify_bank_transfer,
    'notification_setting.notify_invoice': s.notify_invoice,
  }
}

export function AdminNotificationSection({
  defaultValues,
}: AdminNotificationSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const formDefaults = buildDefaults(defaultValues)
  const baselineRef = useRef<Record<DotKey, string | boolean>>(
    flatten(formDefaults)
  )
  const [testing, setTesting] = useState<'wechat_work' | 'dingtalk' | null>(
    null
  )

  const form = useForm<NotificationFormValues>({
    resolver: zodResolver(notificationSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: NotificationFormValues) => {
    const flat = flatten(values)
    const updates = DOT_KEYS.filter(
      (key) => flat[key] !== baselineRef.current[key]
    )
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    for (const key of updates) {
      await updateOption.mutateAsync({ key, value: flat[key] })
    }
    baselineRef.current = flat
  }

  const handleTest = async (channel: 'wechat_work' | 'dingtalk') => {
    setTesting(channel)
    try {
      const res = await testNotification(channel)
      if (res.success) {
        toast.success(t('Test notification sent'))
      } else {
        toast.error(res.message || i18next.t('Failed to send test notification'))
      }
    } catch (error) {
      toast.error(
        (error as Error).message || i18next.t('Failed to send test notification')
      )
    } finally {
      setTesting(null)
    }
  }

  const eventSwitches: Array<{ name: DotKey; label: string }> = [
    {
      name: 'notification_setting.notify_feedback',
      label: t('New ticket / feedback'),
    },
    {
      name: 'notification_setting.notify_enterprise',
      label: t('Enterprise verification submitted'),
    },
    {
      name: 'notification_setting.notify_kyc',
      label: t('Real-name verification submitted'),
    },
    {
      name: 'notification_setting.notify_bank_transfer',
      label: t('Bank transfer submitted'),
    },
    {
      name: 'notification_setting.notify_invoice',
      label: t('Invoice request submitted'),
    },
  ]

  return (
    <SettingsSection
      title={t('Admin Notifications')}
      description={t(
        'Push a reminder to admins via WeChat Work / DingTalk group bots when users submit items that need review'
      )}
    >
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit(onSubmit)}
          className='space-y-6'
          autoComplete='off'
        >
          <FormField
            control={form.control}
            name='notification_setting.wechat_work_webhook_url'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('WeChat Work Webhook URL')}</FormLabel>
                <div className='flex gap-2'>
                  <FormControl>
                    <Input
                      autoComplete='off'
                      placeholder='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
                      {...field}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={testing !== null}
                    onClick={() => handleTest('wechat_work')}
                  >
                    {testing === 'wechat_work'
                      ? t('Sending...')
                      : t('Send test')}
                  </Button>
                </div>
                <FormDescription>
                  {t('Group robot webhook URL. Leave blank to disable.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='notification_setting.dingtalk_webhook_url'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('DingTalk Webhook URL')}</FormLabel>
                <div className='flex gap-2'>
                  <FormControl>
                    <Input
                      autoComplete='off'
                      placeholder='https://oapi.dingtalk.com/robot/send?access_token=...'
                      {...field}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={testing !== null}
                    onClick={() => handleTest('dingtalk')}
                  >
                    {testing === 'dingtalk' ? t('Sending...') : t('Send test')}
                  </Button>
                </div>
                <FormDescription>
                  {t(
                    'Group robot webhook URL. Add "管理员通知" as a custom keyword (or allowlist the server IP) on the DingTalk side. Leave blank to disable.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='space-y-3'>
            <p className='text-sm font-medium'>{t('Events to notify')}</p>
            <div className='grid gap-4 md:grid-cols-2'>
              {eventSwitches.map((item) => (
                <FormField
                  key={item.name}
                  control={form.control}
                  name={item.name}
                  render={({ field }) => (
                    <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                      <FormLabel className='text-base'>{item.label}</FormLabel>
                      <FormControl>
                        <Switch
                          checked={Boolean(field.value)}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
              ))}
            </div>
          </div>

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending
              ? t('Saving...')
              : t('Save notification settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}
