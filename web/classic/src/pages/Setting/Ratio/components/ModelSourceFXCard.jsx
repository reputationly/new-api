import React, { useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Input,
  Modal,
  Space,
  Spin,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../../helpers';

const endpoint = '/api/model_pricing/source_fx';
const dateLabel = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  month: '2-digit',
  day: '2-digit',
});
const dateTime = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  dateStyle: 'short',
  timeStyle: 'short',
});

function FXChart({ series, t }) {
  const [hoveredDate, setHoveredDate] = useState(null);
  const { from, through, quotes, baseline, changes } = series;
  const source = quotes.map(({ date, rate }) => ({
    at: Date.parse(date + 'T09:15:00+08:00') / 1000,
    rate,
    date,
  }));
  const configured = [{ at: from, rate: baseline?.rate }, ...changes];
  const rates = [...source, ...configured]
    .map(({ rate }) => rate)
    .filter(Number.isFinite);
  if (!rates.length) return null;

  const width = 700;
  const height = 174;
  const startX = 54;
  const endX = width - 18;
  const startY = 12;
  const endY = height - 28;
  const min = Math.min(...rates);
  const max = Math.max(...rates);
  const padding = Math.max((max - min) * 0.1, 0.005);
  const low = min - padding;
  const high = max + padding;
  const x = (at) => startX + ((endX - startX) * (at - from)) / (through - from);
  const y = (rate) => endY - ((endY - startY) * (rate - low)) / (high - low);
  const coords = (at, rate) => `${x(at)},${y(rate)}`;
  const cfetsLine = source.map(({ at, rate }) => coords(at, rate)).join(' ');
  const stepPath = configured
    .map(({ at, rate }, i) => {
      if (!Number.isFinite(rate)) return '';
      const previous = configured[i - 1]?.rate;
      const next = configured[i + 1]?.at ?? through;
      return [
        Number.isFinite(previous)
          ? `L ${coords(at, rate)}`
          : `M ${coords(at, rate)}`,
        `L ${coords(next, rate)}`,
      ].join(' ');
    })
    .join(' ');
  const hovered = source.find(({ date }) => date === hoveredDate);
  const configuredRate = hovered
    ? configured.findLast(
        ({ at }) => at <= Date.parse(hovered.date + 'T23:59:59+08:00') / 1000,
      )?.rate
    : null;
  const tooltipWidth = 180;
  const onMouseMove = (event) => {
    if (!source.length) return;
    const bounds = event.currentTarget.getBoundingClientRect();
    const scale = Math.min(bounds.width / width, bounds.height / height);
    if (!scale) return;
    const plotX =
      (event.clientX - bounds.left - (bounds.width - width * scale) / 2) /
      scale;
    if (plotX < startX || plotX > endX) {
      setHoveredDate(null);
      return;
    }
    const at = from + ((plotX - startX) * (through - from)) / (endX - startX);
    const nearest = source.reduce((best, point) =>
      Math.abs(point.at - at) < Math.abs(best.at - at) ? point : best,
    );
    setHoveredDate(nearest.date);
  };

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      role='img'
      aria-label={t('近 90 天 CFETS 中间价和配置汇率走势图')}
      className='w-full max-h-52'
      onMouseMove={onMouseMove}
      onMouseLeave={() => setHoveredDate(null)}
    >
      {[high, (high + low) / 2, low].map((rate) => (
        <g key={rate}>
          <line
            x1={startX}
            x2={endX}
            y1={y(rate)}
            y2={y(rate)}
            stroke='var(--semi-color-border)'
          />
          <text
            x={startX - 8}
            y={y(rate) + 4}
            textAnchor='end'
            fontSize='12'
            fill='currentColor'
          >
            {rate.toFixed(3)}
          </text>
        </g>
      ))}
      {[0, 15, 30, 45, 60, 75, 89].map((offset) => {
        const at = from + offset * 86400 + 3600;
        return (
          <text
            key={offset}
            x={x(at)}
            y={height - 6}
            textAnchor='middle'
            fontSize='12'
            fill='currentColor'
          >
            {dateLabel.format(at * 1000)}
          </text>
        );
      })}
      <polyline
        points={cfetsLine}
        fill='none'
        stroke='var(--semi-color-primary)'
        strokeWidth='2'
      />
      {source.map(({ date, at, rate }) => (
        <circle
          key={date}
          cx={x(at)}
          cy={y(rate)}
          r='2.5'
          fill='var(--semi-color-primary)'
        />
      ))}
      <path
        d={stepPath}
        fill='none'
        stroke='var(--semi-color-warning)'
        strokeWidth='2'
      />
      {hovered && (
        <g pointerEvents='none'>
          <line
            x1={x(hovered.at)}
            x2={x(hovered.at)}
            y1={startY}
            y2={endY}
            stroke='currentColor'
            strokeDasharray='3 3'
            opacity='0.35'
          />
          <circle
            cx={x(hovered.at)}
            cy={y(hovered.rate)}
            r='4'
            fill='var(--semi-color-primary)'
          />
          <foreignObject
            x={
              x(hovered.at) + tooltipWidth + 8 <= endX
                ? x(hovered.at) + 8
                : x(hovered.at) - tooltipWidth - 8
            }
            y={startY}
            width={tooltipWidth}
            height='92'
          >
            <div
              role='tooltip'
              className='rounded border p-2 text-xs leading-5 shadow'
              style={{
                background: 'var(--semi-color-bg-2)',
                borderColor: 'var(--semi-color-border)',
                color: 'var(--semi-color-text-0)',
              }}
            >
              <strong>{hovered.date}</strong>
              <div>
                {t('CFETS 中间价')}：{hovered.rate.toFixed(4)}
              </div>
              {Number.isFinite(configuredRate) && (
                <div>
                  {t('当日配置汇率')}：{configuredRate.toFixed(4)}
                </div>
              )}
            </div>
          </foreignObject>
        </g>
      )}
    </svg>
  );
}

export default function ModelSourceFXCard({ configuredRate, onRateChange }) {
  const { t } = useTranslation();
  const [rate, setRate] = useState(configuredRate || '');
  const [draft, setDraft] = useState(configuredRate || '');
  const [series, setSeries] = useState(null);
  const [loadError, setLoadError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const busy = useRef(false);

  useEffect(() => {
    setRate(configuredRate || '');
    setDraft(configuredRate || '');
  }, [configuredRate]);

  const load = async (signal) => {
    try {
      const { data } = await API.get(endpoint, {
        signal,
        disableDuplicate: true,
        skipErrorHandler: true,
      });
      if (signal?.aborted) return;
      setLoadError(!data.success);
      if (data.success) {
        setSeries(data.data);
        const stored = data.data.current_rate || '';
        setRate(stored);
        setDraft((value) => (value === rate ? stored : value));
        onRateChange?.(stored);
      }
    } catch {
      if (!signal?.aborted) setLoadError(true);
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  };

  useEffect(() => {
    const request = new AbortController();
    load(request.signal);
    return () => request.abort();
  }, []);

  const save = () => {
    if (busy.current) return;
    const parsed = Number(draft);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      showError(t('请输入有效的全局模型源汇率'));
      return;
    }
    busy.current = true;
    setSaving(true);
    const previous = Number(rate);
    if (
      previous > 0 &&
      Math.abs(parsed - previous) >
        previous * 0.1 + Math.abs(previous) * Number.EPSILON
    ) {
      Modal.confirm({
        title: t('确认大幅调整模型源汇率'),
        content: `${previous} → ${parsed} ¥/$`,
        onOk: () => persistRate(parsed),
        onCancel: finishSaving,
      });
    } else {
      void persistRate(parsed);
    }
  };

  const finishSaving = () => {
    busy.current = false;
    setSaving(false);
  };

  const persistRate = async (parsed) => {
    try {
      const value = String(parsed);
      const { data } = await API.post('/api/model_pricing/options', {
        options: { ModelSourceRate: value },
      });
      if (!data.success) {
        showError(data.message);
        return;
      }
      setRate(value);
      setDraft(value);
      onRateChange?.(value);
      showSuccess(t('保存成功'));
      await load();
    } catch (error) {
      showError(error.message);
    } finally {
      finishSaving();
    }
  };

  const quotes =
    series?.quotes.filter(({ date }) => date >= series.stats_from) || [];
  const latest = quotes.at(-1);
  const values = quotes.map(({ rate }) => rate);
  const current = Number(rate);
  const deviation = latest && current > 0 && (current / latest.rate - 1) * 100;
  const status = series?.status;
  const lastSuccess = status?.last_success
    ? `${dateTime.format(status.last_success)} (UTC+8)`
    : t('尚无成功记录');
  const lastChange = series?.last_change;
  const latestDate = series?.latest_date;

  return (
    <Card title={t('全局模型源汇率')} className='my-3'>
      <Space wrap>
        <strong>
          {t('当前生效汇率')}：{current > 0 ? `${rate} ¥/$` : t('未设置')}
        </strong>
        <span>
          {t('最后修改')}：
          {lastChange
            ? `${lastChange.actor} · ${dateTime.format(lastChange.at * 1000)} (UTC+8)`
            : t('暂无审计记录')}
        </span>
      </Space>
      <Space wrap className='my-3'>
        <Input
          aria-label={t('全局模型源汇率')}
          suffix='¥/$'
          placeholder='6.8'
          value={draft}
          disabled={saving}
          onChange={setDraft}
          style={{ width: 160 }}
        />
        <Button loading={saving} disabled={saving} onClick={save}>
          {t('保存全局汇率')}
        </Button>
        <span>
          {t('独立保存，只影响新的美元计费和引用 fx 的请求，不修改原价。')}
        </span>
      </Space>
      {loadError && (
        <Banner
          type='warning'
          closeIcon={null}
          description={t('暂时无法读取汇率走势或审计记录。')}
        />
      )}
      {status?.error && (
        <Banner
          type='warning'
          closeIcon={null}
          description={`${t('CFETS 请求失败')}：${status.error}；${t('最后成功拉取')}：${lastSuccess}`}
        />
      )}
      <div className='mt-3'>
        <strong>{t('近 90 天 USD/CNY 汇率')}</strong>
        <span
          className='ml-3 text-xs'
          style={{ color: 'var(--semi-color-primary)' }}
        >
          ● {t('CFETS 中间价')}
        </span>
        <span
          className='ml-3 text-xs'
          style={{ color: 'var(--semi-color-warning)' }}
        >
          ━ {t('配置汇率')}
        </span>
      </div>
      <Spin spinning={loading}>
        <div className='mt-2 text-sm font-semibold'>{t('近 30 天统计')}</div>
        {latest && (
          <Space wrap className='mt-2'>
            <span>
              {t('最新中间价')}：{latest.rate.toFixed(4)} ({latest.date})
            </span>
            <span>
              {t('均值')}：
              {(
                values.reduce((sum, value) => sum + value, 0) / values.length
              ).toFixed(4)}
            </span>
            <span>
              {t('最高')}：{Math.max(...values).toFixed(4)}
            </span>
            <span>
              {t('最低')}：{Math.min(...values).toFixed(4)}
            </span>
            {typeof deviation === 'number' && (
              <span>
                {t('配置偏差')}：{deviation > 0 ? '+' : ''}
                {deviation.toFixed(2)}%
              </span>
            )}
          </Space>
        )}
        {latestDate && latestDate !== series.today && (
          <p className='mt-2 text-xs text-gray-500'>
            {t('最新中间价 {{date}}', {
              date: latestDate.slice(5).replace('-', '/'),
            })}
          </p>
        )}
        {!loading && !latest && <p>{t('近 30 天暂无 CFETS 中间价数据')}</p>}
        {series && <FXChart series={series} t={t} />}
      </Spin>
      <div className='text-xs text-gray-500'>
        {t(
          '来源：CFETS。中间价仅供参考，不会自动更改计费汇率；配置历史按审计记录展示。',
        )}
      </div>
    </Card>
  );
}
