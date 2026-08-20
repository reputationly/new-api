import { useTranslation } from 'react-i18next'
import { SectionPageLayout } from '@/components/layout'
import { ModerationLogsTable } from './components/moderation-logs-table'

export function ModerationLogs() {
  const { t } = useTranslation()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Content Moderation')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Review moderation decisions, matched keywords and blocked content')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <ModerationLogsTable />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
