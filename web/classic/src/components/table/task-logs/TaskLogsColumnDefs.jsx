/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React from 'react';
import { Progress, Tag, Tooltip, Typography } from '@douyinfe/semi-ui';
import {
  Music,
  FileText,
  HelpCircle,
  CheckCircle,
  Pause,
  Clock,
  Play,
  XCircle,
  Loader,
  List,
  Hash,
  Video,
  Sparkles,
} from 'lucide-react';
import {
  TASK_ACTION_FIRST_TAIL_GENERATE,
  TASK_ACTION_GENERATE,
  TASK_ACTION_REFERENCE_GENERATE,
  TASK_ACTION_TEXT_GENERATE,
  TASK_ACTION_REMIX_GENERATE,
} from '../../../constants/common.constant';
import { CHANNEL_OPTIONS } from '../../../constants/channel.constants';
import { stringToColor } from '../../../helpers/render';
import { Avatar, Space } from '@douyinfe/semi-ui';

const colors = [
  'amber',
  'blue',
  'cyan',
  'green',
  'grey',
  'indigo',
  'light-blue',
  'lime',
  'orange',
  'pink',
  'purple',
  'red',
  'teal',
  'violet',
  'yellow',
];

// Render functions
const renderTimestamp = (timestampInSeconds) => {
  const date = new Date(timestampInSeconds * 1000); // 从秒转换为毫秒

  const year = date.getFullYear(); // 获取年份
  const month = ('0' + (date.getMonth() + 1)).slice(-2); // 获取月份，从0开始需要+1，并保证两位数
  const day = ('0' + date.getDate()).slice(-2); // 获取日期，并保证两位数
  const hours = ('0' + date.getHours()).slice(-2); // 获取小时，并保证两位数
  const minutes = ('0' + date.getMinutes()).slice(-2); // 获取分钟，并保证两位数
  const seconds = ('0' + date.getSeconds()).slice(-2); // 获取秒钟，并保证两位数

  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`; // 格式化输出
};

function renderDuration(submit_time, finishTime) {
  if (!submit_time || !finishTime) return 'N/A';
  const durationSec = finishTime - submit_time;
  const color = durationSec > 60 ? 'red' : 'green';

  // 返回带有样式的颜色标签
  return (
    <Tag color={color} shape='circle'>
      {durationSec} s
    </Tag>
  );
}

// gpustackplus 任务:Task.Data 存的是门面提交响应(_public 形态),其中带
// task_type(tts/t2i/i2i/t2v/i2v/flf2v/s2v)。取到就按它精确显示类型,取不到
// (其他平台/旧数据)落回按 action 的原有分支。data 可能是对象或 JSON 字符串;
// suno 的 data 是数组,返回空串走原逻辑。
const facadeTaskType = (record) => {
  let d = record?.data;
  if (!d) return '';
  if (typeof d === 'string') {
    try {
      d = JSON.parse(d);
    } catch (e) {
      return '';
    }
  }
  if (Array.isArray(d) || typeof d !== 'object') return '';
  return typeof d.task_type === 'string' ? d.task_type : '';
};

// 门面 task_type → 展示标签/颜色。标签词必须落在能力标签词表内(后端
// constant/model_capability.go 的 Image/Video/Audio/MusicCapabilities,归属见
// constant/playground_tab.go 的 task_type→tab 表),否则日志里的「类型」与模型广场
// 的能力标签、体验区玩法三者对不上号。新增 task_type 时三处同步。
// 颜色分大类:视频蓝、图片青、语音紫、音乐橙。
const FACADE_TASK_TYPE_META = {
  // 图片。i2i 覆盖「图生图/图像编辑/局部重绘/扩图/高清放大」五个能力标签,任务数据
  // 里无从区分,取大类泛称。
  t2i: { label: '文生图', color: 'cyan' },
  i2i: { label: '图生图', color: 'cyan' },

  // 视频
  t2v: { label: '文生视频', color: 'blue' },
  // 关键帧一格覆盖三态(i2v 首帧 / flf2v 首尾帧 / l2va 尾帧),输入形态不同、排障时
  // 要分得开,故在能力标签「关键帧」后缀细分,而不是三条都显示「关键帧」。
  i2v: { label: '关键帧·首帧', color: 'blue' },
  flf2v: { label: '关键帧·首尾帧', color: 'blue' },
  l2va: { label: '关键帧·尾帧', color: 'blue' },
  // r2v 是 Bernini 纯参考图,归体验区「图生视频」;r2va 是 MiniMax H3 Ref2VA,
  // 归「参考生视频」—— 两者都不是视频编辑。
  r2v: { label: '图生视频', color: 'blue' },
  r2va: { label: '参考生视频', color: 'blue' },
  s2v: { label: '数字人', color: 'blue' },
  // 视频编辑(Bernini):按输入分流的四种玩法,统一显示「视频编辑」。
  v2v: { label: '视频编辑', color: 'blue' },
  rv2v: { label: '视频编辑', color: 'blue' },
  mv2v: { label: '视频编辑', color: 'blue' },
  ads2v: { label: '视频编辑', color: 'blue' },
  // 超分不单独开玩法(只作 1080P 两段流水线的内部一段),故能力词表里没有这一项;
  // 但 sr 是有效 task_type,直连与流水线都会落记录,据实显示。
  sr: { label: '视频超分', color: 'blue' },

  // 语音。四个能力标签(情感合成/语音合成/双人对话/声音设计)共用 task_type=tts,
  // 无从区分,取大类泛称。
  tts: { label: '语音合成', color: 'purple' },
  // 视频配音产物是视频,但能力标签归在语音模型下(入口挂语音页,模型配在
  // VideoModelConfig),故跟语音同色。
  v2a: { label: '视频配音', color: 'purple' },

  // 音乐
  t2m: { label: '文生音乐', color: 'orange' },
  cover: { label: '音乐改编', color: 'orange' },
  repaint: { label: '音乐重绘', color: 'orange' },
  // ── 已下线玩法的只读标签 ─────────────────────────────────────────
  // AudioX(t2a/v2m/tv2m)与 SoulX-Singer(svs)已于 2026-08 下线,写入侧全部摘除,
  // 新任务不会再产生这几个 task_type。**这几行刻意保留**:删了的话历史记录就只显示
  // 原始的 t2a/svs,谁也看不懂那是什么玩法 —— 读侧兼容的成本只有几行映射。
  t2a: { label: '文生音效', color: 'orange' },
  svs: { label: '歌声合成', color: 'orange' },
  // AudioX 视频生音:已无前端入口,但 task_type 仍有效,直连仍可能产生记录。
  // 能力词表里没有对应标签(已随产品线下线),按其契约据实显示。
  v2m: { label: '视频生音乐', color: 'orange' },
  tv2m: { label: '视频生音乐', color: 'orange' },
};

const renderType = (type, t, taskType) => {
  const meta = FACADE_TASK_TYPE_META[taskType];
  if (meta) {
    return (
      <Tag
        color={meta.color}
        shape='circle'
        prefixIcon={<Sparkles size={14} />}
      >
        {t(meta.label)}
      </Tag>
    );
  }
  switch (type) {
    case 'MUSIC':
      return (
        <Tag color='grey' shape='circle' prefixIcon={<Music size={14} />}>
          {t('生成音乐')}
        </Tag>
      );
    case 'LYRICS':
      return (
        <Tag color='pink' shape='circle' prefixIcon={<FileText size={14} />}>
          {t('生成歌词')}
        </Tag>
      );
    case TASK_ACTION_GENERATE:
      return (
        <Tag color='blue' shape='circle' prefixIcon={<Sparkles size={14} />}>
          {t('图生视频')}
        </Tag>
      );
    case TASK_ACTION_TEXT_GENERATE:
      return (
        <Tag color='blue' shape='circle' prefixIcon={<Sparkles size={14} />}>
          {t('文生视频')}
        </Tag>
      );
    case TASK_ACTION_FIRST_TAIL_GENERATE:
      return (
        <Tag color='blue' shape='circle' prefixIcon={<Sparkles size={14} />}>
          {t('首尾生视频')}
        </Tag>
      );
    case TASK_ACTION_REFERENCE_GENERATE:
      return (
        <Tag color='blue' shape='circle' prefixIcon={<Sparkles size={14} />}>
          {t('参照生视频')}
        </Tag>
      );
    case TASK_ACTION_REMIX_GENERATE:
      return (
        <Tag color='blue' shape='circle' prefixIcon={<Sparkles size={14} />}>
          {t('视频Remix')}
        </Tag>
      );
    default:
      return (
        <Tag color='white' shape='circle' prefixIcon={<HelpCircle size={14} />}>
          {t('未知')}
        </Tag>
      );
  }
};

const renderPlatform = (platform, t) => {
  let option = CHANNEL_OPTIONS.find(
    (opt) => String(opt.value) === String(platform),
  );
  if (option) {
    return (
      <Tag color={option.color} shape='circle'>
        {option.label}
      </Tag>
    );
  }
  switch (platform) {
    case 'suno':
      return (
        <Tag color='green' shape='circle'>
          Suno
        </Tag>
      );
    default:
      return (
        <Tag color='white' shape='circle'>
          {t('未知')}
        </Tag>
      );
  }
};

const renderStatus = (type, t) => {
  switch (type) {
    case 'SUCCESS':
      return (
        <Tag
          color='green'
          shape='circle'
          prefixIcon={<CheckCircle size={14} />}
        >
          {t('成功')}
        </Tag>
      );
    case 'NOT_START':
      return (
        <Tag color='grey' shape='circle' prefixIcon={<Pause size={14} />}>
          {t('未启动')}
        </Tag>
      );
    case 'SUBMITTED':
      return (
        <Tag color='yellow' shape='circle' prefixIcon={<Clock size={14} />}>
          {t('队列中')}
        </Tag>
      );
    case 'IN_PROGRESS':
      return (
        <Tag color='blue' shape='circle' prefixIcon={<Play size={14} />}>
          {t('执行中')}
        </Tag>
      );
    case 'FAILURE':
      return (
        <Tag color='red' shape='circle' prefixIcon={<XCircle size={14} />}>
          {t('失败')}
        </Tag>
      );
    case 'QUEUED':
      return (
        <Tag color='orange' shape='circle' prefixIcon={<List size={14} />}>
          {t('排队中')}
        </Tag>
      );
    case 'UNKNOWN':
      return (
        <Tag color='white' shape='circle' prefixIcon={<HelpCircle size={14} />}>
          {t('未知')}
        </Tag>
      );
    case '':
      return (
        <Tag color='grey' shape='circle' prefixIcon={<Loader size={14} />}>
          {t('正在提交')}
        </Tag>
      );
    default:
      return (
        <Tag color='white' shape='circle' prefixIcon={<HelpCircle size={14} />}>
          {t('未知')}
        </Tag>
      );
  }
};

export const getTaskLogsColumns = ({
  t,
  COLUMN_KEYS,
  copyText,
  openContentModal,
  isAdminUser,
  openVideoModal,
  openAudioModal,
}) => {
  return [
    {
      key: COLUMN_KEYS.SUBMIT_TIME,
      title: t('提交时间'),
      dataIndex: 'submit_time',
      render: (text, record, index) => {
        return <div>{text ? renderTimestamp(text) : '-'}</div>;
      },
    },
    {
      key: COLUMN_KEYS.FINISH_TIME,
      title: t('结束时间'),
      dataIndex: 'finish_time',
      render: (text, record, index) => {
        return <div>{text ? renderTimestamp(text) : '-'}</div>;
      },
    },
    {
      key: COLUMN_KEYS.DURATION,
      title: t('花费时间'),
      dataIndex: 'finish_time',
      render: (finish, record) => {
        return <>{finish ? renderDuration(record.submit_time, finish) : '-'}</>;
      },
    },
    {
      key: COLUMN_KEYS.CHANNEL,
      title: t('渠道'),
      dataIndex: 'channel_id',
      render: (text, record, index) => {
        return isAdminUser ? (
          <div>
            <Tag
              color={colors[parseInt(text) % colors.length]}
              size='large'
              shape='circle'
              onClick={() => {
                copyText(text);
              }}
            >
              {text}
            </Tag>
          </div>
        ) : (
          <></>
        );
      },
    },
    {
      key: COLUMN_KEYS.USERNAME,
      title: t('用户'),
      dataIndex: 'username',
      render: (userId, record, index) => {
        if (!isAdminUser) {
          return <></>;
        }
        const displayText = String(record.username || userId || '?');
        return (
          <Space>
            <Avatar size='extra-small' color={stringToColor(displayText)}>
              {displayText.slice(0, 1)}
            </Avatar>
            <Typography.Text>{displayText}</Typography.Text>
          </Space>
        );
      },
    },
    {
      key: COLUMN_KEYS.PLATFORM,
      title: t('平台'),
      dataIndex: 'platform',
      render: (text, record, index) => {
        return <div>{renderPlatform(text, t)}</div>;
      },
    },
    {
      key: COLUMN_KEYS.TYPE,
      title: t('类型'),
      dataIndex: 'action',
      render: (text, record, index) => {
        return <div>{renderType(text, t, facadeTaskType(record))}</div>;
      },
    },
    {
      key: COLUMN_KEYS.TASK_ID,
      title: t('任务ID'),
      dataIndex: 'task_id',
      render: (text, record, index) => {
        return (
          <Typography.Text
            ellipsis={{ showTooltip: true }}
            onClick={() => {
              openContentModal(JSON.stringify(record, null, 2));
            }}
          >
            <div>{text}</div>
          </Typography.Text>
        );
      },
    },
    {
      key: COLUMN_KEYS.TASK_STATUS,
      title: t('任务状态'),
      dataIndex: 'status',
      render: (text, record, index) => {
        return <div>{renderStatus(text, t)}</div>;
      },
    },
    {
      key: COLUMN_KEYS.PROGRESS,
      title: t('进度'),
      dataIndex: 'progress',
      render: (text, record, index) => {
        return (
          <div>
            {isNaN(text?.replace('%', '')) ? (
              text || '-'
            ) : (
              <Progress
                stroke={
                  record.status === 'FAILURE'
                    ? 'var(--semi-color-warning)'
                    : null
                }
                percent={text ? parseInt(text.replace('%', '')) : 0}
                showInfo={true}
                aria-label='task progress'
                style={{ minWidth: '160px' }}
              />
            )}
          </div>
        );
      },
    },
    {
      key: COLUMN_KEYS.FAIL_REASON,
      title: t('详情'),
      dataIndex: 'fail_reason',
      fixed: 'right',
      render: (text, record, index) => {
        // Suno audio preview
        const isSunoSuccess =
          record.platform === 'suno' &&
          record.status === 'SUCCESS' &&
          Array.isArray(record.data) &&
          record.data.some((c) => c.audio_url);
        if (isSunoSuccess) {
          return (
            <a
              href='#'
              onClick={(e) => {
                e.preventDefault();
                openAudioModal(record.data);
              }}
            >
              {t('点击预览音乐')}
            </a>
          );
        }

        // 视频预览：优先使用 result_url，兼容旧数据 fail_reason 中的 URL
        const isVideoTask =
          record.action === TASK_ACTION_GENERATE ||
          record.action === TASK_ACTION_TEXT_GENERATE ||
          record.action === TASK_ACTION_FIRST_TAIL_GENERATE ||
          record.action === TASK_ACTION_REFERENCE_GENERATE ||
          record.action === TASK_ACTION_REMIX_GENERATE;
        const isSuccess = record.status === 'SUCCESS';
        const resultUrl = record.result_url;
        const hasResultUrl =
          typeof resultUrl === 'string' && /^https?:\/\//.test(resultUrl);
        // 语音合成(tts):成品是 .wav,内联音频播放器(视频弹窗对 wav 只会黑屏出声)
        if (isSuccess && hasResultUrl && facadeTaskType(record) === 'tts') {
          return (
            <audio
              controls
              preload='none'
              src={resultUrl}
              style={{ height: 28, width: 200 }}
            />
          );
        }
        if (isSuccess && isVideoTask && hasResultUrl) {
          return (
            <a
              href='#'
              onClick={(e) => {
                e.preventDefault();
                openVideoModal(resultUrl, record.task_id);
              }}
            >
              {t('点击预览视频')}
            </a>
          );
        }
        if (!text) {
          return t('无');
        }
        return (
          <Typography.Text
            ellipsis={{ showTooltip: true }}
            style={{ width: 100 }}
            onClick={() => {
              openContentModal(text);
            }}
          >
            {text}
          </Typography.Text>
        );
      },
    },
  ];
};
