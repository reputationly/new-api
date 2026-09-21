import React, { useState } from 'react';
import { Tabs, TabPane, Typography } from '@douyinfe/semi-ui';
import { IconFile } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import CardPro from '../../components/common/ui/CardPro';
import UploadCard from '../../components/table/reconcile/UploadCard';
import SummaryCard from '../../components/table/reconcile/SummaryCard';
import DiffTable from '../../components/table/reconcile/DiffTable';
import ByModelTable from '../../components/table/reconcile/ByModelTable';
import ParseErrorsList from '../../components/table/reconcile/ParseErrorsList';
import useReconcileUpload from '../../hooks/reconcile/useReconcileUpload';
import FundReconcilePanel from '../../components/table/reconcile/FundReconcilePanel';

const { Text } = Typography;

// `.table-scroll-card` (defined in src/index.css) forces every CardPro to
// `height: calc(100vh - 110px)` so it fills the viewport — fine for the
// single-card pages it was written for (KYC, Logs), wrong for this page
// which stacks 4 cards vertically. Override with !h-auto / !max-h-none so
// each card sizes to its content and the page scrolls normally.
const STACKED_CARD_CLASS = '!h-auto !max-h-none';

export default function ReconcilePage() {
  const { t } = useTranslation();
  const u = useReconcileUpload();
  const r = u.result;
  // 收入对账（收客户多少）与成本对账（付供应商多少）是两件正交的事，
  // 合在一页靠 tab 切换，顶层再看利润。默认落在收入侧：它是日常高频查看的。
  const [tab, setTab] = useState('revenue');

  if (tab !== 'cost') {
    return (
      <div className='mt-[60px] px-2 flex flex-col gap-3'>
        <ReconcileTabs tab={tab} setTab={setTab} t={t} />
        <FundReconcilePanel />
      </div>
    );
  }

  return (
    <div className='mt-[60px] px-2 flex flex-col gap-3'>
      <ReconcileTabs tab={tab} setTab={setTab} t={t} />
      <CardPro
        type='type1'
        className={STACKED_CARD_CLASS}
        descriptionArea={
          <div className='flex items-center text-blue-500'>
            <IconFile className='mr-2' />
            <Text>{t('对账管理')}</Text>
          </div>
        }
        t={t}
      >
        <UploadCard
          channels={u.channels}
          selectedChannelIds={u.selectedChannelIds}
          setSelectedChannelIds={u.setSelectedChannelIds}
          file={u.file}
          setFile={u.setFile}
          granularity={u.granularity}
          setGranularity={u.setGranularity}
          uploading={u.uploading}
          onSubmit={u.submit}
          onReset={u.reset}
        />
      </CardPro>

      {r && (
        <>
          <CardPro
            type='type1'
            className={STACKED_CARD_CLASS}
            descriptionArea={<Text strong>{t('对账总览')}</Text>}
            t={t}
          >
            <SummaryCard summary={r.summary} drift={r.drift_analysis} />
          </CardPro>

          {r.parse_errors && r.parse_errors.length > 0 && (
            <ParseErrorsList errors={r.parse_errors} />
          )}

          <CardPro
            type='type1'
            className={STACKED_CARD_CLASS}
            descriptionArea={<Text strong>{t('按模型汇总')}</Text>}
            t={t}
          >
            <ByModelTable byModel={r.by_model} />
          </CardPro>

          <CardPro
            type='type1'
            className={STACKED_CARD_CLASS}
            descriptionArea={
              <Text strong>{t('明细差异（仅真实差异，精确到小时）')}</Text>
            }
            t={t}
          >
            <DiffTable rows={r.rows} />
          </CardPro>
        </>
      )}
    </div>
  );
}

function ReconcileTabs({ tab, setTab, t }) {
  return (
    <Tabs type='line' activeKey={tab} onChange={setTab}>
      <TabPane tab={t('收入对账')} itemKey='revenue' />
      <TabPane tab={t('成本对账')} itemKey='cost' />
    </Tabs>
  );
}
