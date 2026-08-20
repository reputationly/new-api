import z from 'zod'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { ModerationLogs } from '@/features/moderation-logs'

const moderationLogsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(50),
  startTime: z.number().optional().catch(undefined),
  endTime: z.number().optional().catch(undefined),
  username: z.string().optional().catch(undefined),
  word: z.string().optional().catch(undefined),
  action: z.string().optional().catch(undefined),
  source: z.string().optional().catch(undefined),
  category: z.string().optional().catch(undefined),
  modelName: z.string().optional().catch(undefined),
  group: z.string().optional().catch(undefined),
  channelId: z.string().optional().catch(undefined),
  requestId: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/_authenticated/moderation-logs/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    // 页面上有被拦原文的入口，非管理员一律挡在路由层，不靠接口兜底。
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  validateSearch: moderationLogsSearchSchema,
  component: ModerationLogs,
})
