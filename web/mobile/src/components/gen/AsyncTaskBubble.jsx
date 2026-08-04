import React from 'react';
import { Button, ProgressCircle, SpinLoading } from 'antd-mobile';

// 视频/音乐/TTS 共用的异步任务助手气泡：排队/进行中/失败/超时四态，
// 完成态由调用方 renderResult 渲染（video / audio 标签不同）。
const AsyncTaskBubble = ({
  m,
  doneStatus,
  resultUrl,
  renderResult,
  onRetry,
  onRefetch,
}) => {
  if (m.status === doneStatus && resultUrl) {
    return renderResult(resultUrl);
  }
  // 已完成却拿不到结果地址(本地缓存丢失且无 taskId 可重建,或后端已按 TTL 清理):
  // 如实说失效。绝不能落到下面的进度分支——那会把一条早就生成好的任务显示成永远停在
  // 上次进度的「生成中」,用户既等不到也无从重来。
  if (m.status === doneStatus) {
    return (
      <div>
        <div style={{ color: 'var(--adm-color-weak)' }}>
          结果已失效，请重新生成
        </div>
        {onRetry && (
          <Button
            size='mini'
            fill='outline'
            style={{ marginTop: 8 }}
            onClick={onRetry}
          >
            重新生成
          </Button>
        )}
      </div>
    );
  }
  if (m.status === 'failed' || m.status === 'canceled') {
    return (
      <div>
        <div style={{ color: 'var(--adm-color-danger)' }}>
          生成失败{m.error ? `：${m.error}` : ''}
        </div>
        {onRetry && (
          <Button
            size='mini'
            fill='outline'
            style={{ marginTop: 8 }}
            onClick={onRetry}
          >
            重试
          </Button>
        )}
      </div>
    );
  }
  // 未完成时进度封顶 99%:上游偶尔会在终态落地前先报 100,「100% 还在生成中」看起来
  // 就是卡死了。100% 只留给真正完成的那一刻(此时已走上面的结果分支)。
  const percent =
    typeof m.progress === 'number' ? Math.min(99, Math.round(m.progress)) : 0;
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      {percent > 0 ? (
        <ProgressCircle percent={percent}>{percent}%</ProgressCircle>
      ) : (
        <SpinLoading style={{ '--size': '24px' }} />
      )}
      <div>
        <div>{m.status === 'queued' ? '排队中…' : '生成中…'}</div>
        {m.pollTimedOut && onRefetch && (
          <Button
            size='mini'
            fill='outline'
            style={{ marginTop: 6 }}
            onClick={onRefetch}
          >
            查询结果
          </Button>
        )}
      </div>
    </div>
  );
};

export default AsyncTaskBubble;
