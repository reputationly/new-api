import React, { useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Col,
  Descriptions,
  Row,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { VChart } from '@visactor/react-vchart';
import {
  API,
  showError,
  showSuccess,
  timestamp2string,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

const { Text, Title } = Typography;

// 与后端 service.HumanizeBytes 同口径（1024 进制）。
function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let f = bytes;
  let i = 0;
  while (f >= 1024 && i < units.length - 1) {
    f /= 1024;
    i++;
  }
  return `${f.toFixed(2)} ${units[i]}`;
}

function levelTag(level, t) {
  switch (level) {
    case 'critical':
      return <Tag color='red'>critical</Tag>;
    case 'warn':
      return <Tag color='orange'>warn</Tag>;
    default:
      return <Tag color='green'>ok</Tag>;
  }
}

// 系统设置 → 媒体存储(OBS)整体用量视图（设计 §12.6）。root 可见。
export default function MediaStorageStats() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [data, setData] = useState(null);

  async function fetchStats() {
    setLoading(true);
    try {
      const res = await API.get('/api/media-store/stats');
      if (res.data.success) {
        setData(res.data.data);
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(t('获取用量失败'));
    } finally {
      setLoading(false);
    }
  }

  async function refreshSnapshot() {
    setRefreshing(true);
    try {
      const res = await API.post('/api/media-store/snapshot');
      if (res.data.success) {
        showSuccess(t('已刷新快照'));
        await fetchStats();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(t('刷新失败'));
    } finally {
      setRefreshing(false);
    }
  }

  useEffect(() => {
    fetchStats();
  }, []);

  // 近 7 天趋势图:每个快照(默认每小时)一个采样点,按时间升序;悬停显示用量/对象数。
  const trendValues = useMemo(
    () =>
      (data?.trend_7d || [])
        .slice()
        .sort((a, b) => a.at - b.at)
        .map((p) => ({
          // 'MM-DD HH:mm',7 天窗口内足够区分且不至于撑爆 x 轴
          time: timestamp2string(p.at).slice(5, 16),
          fullTime: timestamp2string(p.at),
          bytes: p.bytes,
          objects: p.objects,
        })),
    [data],
  );

  const trendSpec = useMemo(
    () => ({
      type: 'line',
      height: 260,
      data: [{ id: 'obsTrend', values: trendValues }],
      xField: 'time',
      yField: 'bytes',
      axes: [
        {
          orient: 'left',
          label: { formatMethod: (v) => formatBytes(v) },
        },
        {
          orient: 'bottom',
          label: { autoHide: true, autoRotate: true },
        },
      ],
      line: { style: { lineWidth: 2 } },
      // 每个采样点画一个小圆点,悬停即出值
      point: { visible: true, style: { size: 5 } },
      crosshair: { xField: { visible: true } },
      tooltip: {
        mark: {
          title: { value: (datum) => datum.fullTime },
          content: [
            { key: t('用量'), value: (datum) => formatBytes(datum.bytes) },
            { key: t('对象数'), value: (datum) => datum.objects },
          ],
        },
        dimension: {
          title: { value: (datum) => datum.fullTime },
          content: [
            { key: t('用量'), value: (datum) => formatBytes(datum.bytes) },
            { key: t('对象数'), value: (datum) => datum.objects },
          ],
        },
      },
    }),
    [trendValues, t],
  );

  return (
    <Card style={{ marginTop: '10px' }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 12,
        }}
      >
        <Title heading={5} style={{ margin: 0 }}>
          {t('媒体存储用量（OBS）')}
        </Title>
        <div style={{ display: 'flex', gap: 8 }}>
          <Button onClick={fetchStats}>{t('刷新')}</Button>
          <Button theme='solid' loading={refreshing} onClick={refreshSnapshot}>
            {t('立即采集快照')}
          </Button>
        </div>
      </div>

      <Spin spinning={loading}>
        {data && !data.enabled && (
          <Banner
            type='warning'
            description={t('媒体存储未启用，用量数据可能为空。')}
            style={{ marginBottom: 12 }}
          />
        )}
        {data && data.snapshot_at === 0 ? (
          <Banner
            type='info'
            description={t(
              '暂无快照数据。cron 会按配置的间隔自动采集，或点击「立即采集快照」。',
            )}
          />
        ) : (
          data && (
            <>
              <Row gutter={16} style={{ marginBottom: 16 }}>
                <Col xs={24} sm={8}>
                  <Card>
                    <Text type='tertiary'>{t('当前总用量')}</Text>
                    <Title heading={3} style={{ marginTop: 4 }}>
                      {data.total_bytes_h || formatBytes(data.total_bytes)}
                    </Title>
                  </Card>
                </Col>
                <Col xs={24} sm={8}>
                  <Card>
                    <Text type='tertiary'>{t('对象数')}</Text>
                    <Title heading={3} style={{ marginTop: 4 }}>
                      {data.total_objects}
                    </Title>
                  </Card>
                </Col>
                <Col xs={24} sm={8}>
                  <Card>
                    <Text type='tertiary'>{t('24h 增长')}</Text>
                    <Title heading={3} style={{ marginTop: 4 }}>
                      {data.growth_24h_h || formatBytes(data.growth_24h_bytes)}
                    </Title>
                  </Card>
                </Col>
              </Row>

              <Descriptions
                row
                size='small'
                style={{ marginBottom: 16 }}
                data={[
                  { key: t('桶名'), value: data.bucket || '-' },
                  {
                    key: t('当前等级'),
                    value: levelTag(data.alert_level, t),
                  },
                  {
                    key: t('warn / critical 阈值'),
                    value: `${data.thresholds?.warn ?? '-'} TB / ${data.thresholds?.critical ?? '-'} TB`,
                  },
                  {
                    key: t('最近快照'),
                    value: data.snapshot_at
                      ? timestamp2string(data.snapshot_at)
                      : '-',
                  },
                  {
                    key: t('最近告警'),
                    value: data.last_alert_at
                      ? `${timestamp2string(data.last_alert_at)}（${data.last_alert_level}）`
                      : t('无'),
                  },
                ]}
              />

              <Title heading={6} style={{ marginBottom: 8 }}>
                {t('7 天趋势')}
              </Title>
              {trendValues.length > 0 ? (
                <VChart
                  spec={trendSpec}
                  option={{ mode: 'desktop-browser' }}
                />
              ) : (
                <Text type='tertiary'>{t('暂无趋势数据')}</Text>
              )}
            </>
          )
        )}
      </Spin>
    </Card>
  );
}
