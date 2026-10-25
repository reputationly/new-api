import React, { useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../helpers';
import { useTranslation } from 'react-i18next';

const DEFAULT_TEMPLATE = {
  name: '默认',
  text: 'AI生成',
  position: 'bottom_right',
  font_scale: 0.04,
  opacity: 0.6,
  color: '#FFFFFF',
};

function parseJSON(raw, fallback) {
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw);
    return parsed ?? fallback;
  } catch (e) {
    return fallback;
  }
}

function optionList(templates, inherited = false) {
  return [
    ...(inherited ? [{ label: '继承默认', value: '' }] : []),
    { label: '不加水印', value: 'none' },
    ...templates.map((template) => ({
      label: template.name,
      value: template.name,
    })),
  ];
}

export default function SettingsWatermark(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [templates, setTemplates] = useState([DEFAULT_TEMPLATE]);
  const [defaultBinding, setDefaultBinding] = useState({
    image: '默认',
    video: '默认',
  });
  const [groupBindings, setGroupBindings] = useState({});
  const [groups, setGroups] = useState([]);
  const [status, setStatus] = useState(null);
  const availableGroups = useMemo(
    () =>
      groups.map((group) =>
        typeof group === 'string' ? group : group.name || group.value,
      ),
    [groups],
  );
  const groupRows = [
    ...availableGroups.map((name) => ({ name, orphan: false })),
    ...Object.keys(groupBindings)
      .filter((name) => !availableGroups.includes(name))
      .map((name) => ({ name, orphan: true })),
  ];

  useEffect(() => {
    const parsedTemplates = parseJSON(props.options?.['watermark.templates'], [
      DEFAULT_TEMPLATE,
    ]);
    const parsedDefault = parseJSON(
      props.options?.['watermark.default_binding'],
      { image: '默认', video: '默认' },
    );
    const parsedGroups = parseJSON(
      props.options?.['watermark.group_bindings'],
      {},
    );
    setEnabled(
      props.options?.['watermark.enabled'] === true ||
        props.options?.['watermark.enabled'] === 'true',
    );
    setTemplates(
      Array.isArray(parsedTemplates) ? parsedTemplates : [DEFAULT_TEMPLATE],
    );
    setDefaultBinding(parsedDefault || { image: '', video: '' });
    setGroupBindings(
      parsedGroups && typeof parsedGroups === 'object' ? parsedGroups : {},
    );
  }, [props.options]);

  useEffect(() => {
    API.get('/api/group/')
      .then((res) => {
        if (res.data?.success) setGroups(res.data.data || []);
      })
      .catch(() => {});
    refreshStatus();
  }, []);

  async function refreshStatus() {
    try {
      const res = await API.get('/api/watermark/status');
      if (res.data?.success) setStatus(res.data.data || {});
    } catch (e) {
      setStatus(null);
    }
  }

  function updateTemplate(index, field, value) {
    setTemplates((previous) =>
      previous.map((template, i) =>
        i === index ? { ...template, [field]: value } : template,
      ),
    );
  }

  function addTemplate() {
    setTemplates((previous) => [
      ...previous,
      {
        ...DEFAULT_TEMPLATE,
        name: `${t('新模板')}${previous.length + 1}`,
      },
    ]);
  }

  function updateDefaultBinding(mediaKind, value) {
    setDefaultBinding((previous) => ({ ...previous, [mediaKind]: value }));
  }

  function updateGroupBinding(group, mediaKind, value) {
    setGroupBindings((previous) => {
      const next = { ...previous };
      const binding = { ...(next[group] || {}), [mediaKind]: value };
      if (!binding.image && !binding.video) delete next[group];
      else next[group] = binding;
      return next;
    });
  }

  async function preview(template) {
    try {
      const res = await API.post('/api/watermark/preview', template);
      if (!res.data?.success) {
        showError(res.data?.message || t('预览失败'));
        return;
      }
      Modal.info({
        title: t('水印预览'),
        width: 720,
        content: (
          <img
            src={res.data.data?.data_url}
            alt={t('水印预览')}
            style={{ display: 'block', width: '100%' }}
          />
        ),
      });
    } catch (e) {
      showError(t('预览失败，请检查 ffmpeg 和字体配置'));
    }
  }

  async function save() {
    setLoading(true);
    try {
      const res = await API.put('/api/watermark/config', {
        enabled,
        templates,
        default_binding: defaultBinding,
        group_bindings: groupBindings,
      });
      if (!res.data?.success) {
        showError(res.data?.message || t('保存失败'));
        return;
      }
      showSuccess(t('保存成功'));
      props.refresh();
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  }

  const templateColumns = [
    {
      title: t('名称'),
      dataIndex: 'name',
      width: 145,
      render: (value, _, index) => (
        <Input
          value={value}
          onChange={(v) => updateTemplate(index, 'name', v)}
        />
      ),
    },
    {
      title: t('水印文案'),
      dataIndex: 'text',
      render: (value, _, index) => (
        <Input
          value={value}
          onChange={(v) => updateTemplate(index, 'text', v)}
        />
      ),
    },
    {
      title: t('位置'),
      dataIndex: 'position',
      width: 160,
      render: (value, _, index) => (
        <Select
          value={value}
          style={{ width: '100%' }}
          onChange={(v) => updateTemplate(index, 'position', v)}
        >
          {[
            ['top_left', '左上'],
            ['top_right', '右上'],
            ['bottom_left', '左下'],
            ['bottom_right', '右下'],
            ['center', '居中'],
          ].map(([v, label]) => (
            <Select.Option key={v} value={v}>
              {t(label)}
            </Select.Option>
          ))}
        </Select>
      ),
    },
    {
      title: t('字号比例'),
      dataIndex: 'font_scale',
      width: 120,
      render: (value, _, index) => (
        <InputNumber
          value={value}
          min={0.01}
          max={0.2}
          step={0.01}
          onChange={(v) => updateTemplate(index, 'font_scale', v)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: t('透明度'),
      dataIndex: 'opacity',
      width: 110,
      render: (value, _, index) => (
        <InputNumber
          value={value}
          min={0.05}
          max={1}
          step={0.05}
          onChange={(v) => updateTemplate(index, 'opacity', v)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: t('颜色'),
      dataIndex: 'color',
      width: 145,
      render: (value, _, index) => (
        <Space>
          <input
            aria-label={t('水印颜色')}
            type='color'
            value={/^#[0-9a-f]{6}$/i.test(value) ? value : '#FFFFFF'}
            onChange={(event) =>
              updateTemplate(index, 'color', event.target.value.toUpperCase())
            }
          />
          <Typography.Text>{value}</Typography.Text>
        </Space>
      ),
    },
    {
      title: t('操作'),
      width: 150,
      render: (_, template, index) => (
        <Space>
          <Button size='small' onClick={() => preview(template)}>
            {t('预览')}
          </Button>
          <Button
            size='small'
            type='danger'
            onClick={() =>
              setTemplates((previous) => previous.filter((_, i) => i !== index))
            }
          >
            {t('删除')}
          </Button>
        </Space>
      ),
    },
  ];

  const bindingOptions = optionList(templates);
  const inheritedOptions = optionList(templates, true);
  const groupColumns = [
    {
      title: t('分组'),
      dataIndex: 'name',
      render: (name, row) => (
        <Space>
          <Typography.Text>{name}</Typography.Text>
          {row.orphan && <Tag color='orange'>{t('孤儿绑定')}</Tag>}
        </Space>
      ),
    },
    ...[
      ['image', '图片水印'],
      ['video', '视频水印'],
    ].map(([kind, title]) => ({
      title: t(title),
      render: (_, row) => (
        <Select
          value={groupBindings[row.name]?.[kind] || ''}
          optionList={inheritedOptions}
          style={{ width: '100%' }}
          onChange={(value) => updateGroupBinding(row.name, kind, value)}
        />
      ),
    })),
  ];

  const failureColumns = [
    {
      title: t('时间'),
      dataIndex: 'created_at',
      width: 170,
      render: (value) => new Date(value * 1000).toLocaleString(),
    },
    { title: t('来源'), dataIndex: 'source', width: 130 },
    { title: t('类型'), dataIndex: 'media_kind', width: 90 },
    { title: t('分组'), dataIndex: 'group_name', width: 120 },
    { title: t('用户'), dataIndex: 'user_id', width: 80 },
    { title: t('任务'), dataIndex: 'task_id', width: 140 },
    { title: t('错误'), dataIndex: 'error' },
  ];

  return (
    <Card style={{ marginTop: 10 }}>
      <Typography.Title heading={6} style={{ marginBottom: 12 }}>
        {t('图片/视频文字水印')}
      </Typography.Title>
      {status && !status.available && (
        <Banner
          type='danger'
          description={t(
            '水印环境未就绪：请检查 ffmpeg、ffprobe 和中文字体安装。',
          )}
          style={{ marginBottom: 12 }}
        />
      )}
      {status?.failures_24h > 0 && (
        <Banner
          type='warning'
          description={t('近 24 小时有水印处理失败，请检查下方失败记录。')}
          style={{ marginBottom: 12 }}
        />
      )}
      {status && (
        <Space style={{ marginBottom: 12 }}>
          <Tag color={status.ffmpeg_ready ? 'green' : 'red'}>
            {t('FFmpeg')}：{status.ffmpeg_ready ? t('就绪') : t('未就绪')}
          </Tag>
          <Tag color={status.font_ready ? 'green' : 'red'}>
            {t('字体')}：{status.font_ready ? t('就绪') : t('未就绪')}
          </Tag>
          <Typography.Text type='tertiary'>
            {t('失败数')}：24h {status.failures_24h || 0} / 7d{' '}
            {status.failures_7d || 0}
          </Typography.Text>
        </Space>
      )}
      {!!status?.recent?.length && (
        <Table
          size='small'
          columns={failureColumns}
          dataSource={status.recent}
          rowKey='id'
          pagination={false}
          style={{ marginBottom: 16 }}
        />
      )}
      <Space style={{ marginBottom: 12 }}>
        <Typography.Text>{t('启用水印')}</Typography.Text>
        <Switch checked={enabled} onChange={setEnabled} />
      </Space>
      <Typography.Title heading={6}>{t('水印模板')}</Typography.Title>
      <Table
        size='small'
        columns={templateColumns}
        dataSource={templates}
        rowKey={(_, index) => index}
        pagination={false}
      />
      <Space style={{ marginTop: 12, marginBottom: 20 }}>
        <Button onClick={addTemplate}>{t('新增模板')}</Button>
      </Space>
      <Typography.Title heading={6}>{t('默认绑定')}</Typography.Title>
      <div style={{ display: 'flex', gap: 16, marginBottom: 20 }}>
        {[
          ['image', '图片'],
          ['video', '视频'],
        ].map(([kind, label]) => (
          <label key={kind} style={{ flex: 1 }}>
            <Typography.Text>{t(label)}</Typography.Text>
            <Select
              value={defaultBinding[kind] || ''}
              optionList={bindingOptions}
              style={{ width: '100%' }}
              onChange={(value) => updateDefaultBinding(kind, value)}
            />
          </label>
        ))}
      </div>
      <Typography.Title heading={6}>{t('分组绑定')}</Typography.Title>
      <Table
        size='small'
        columns={groupColumns}
        dataSource={groupRows}
        rowKey='name'
        pagination={false}
      />
      <div style={{ marginTop: 16 }}>
        <Button type='primary' loading={loading} onClick={save}>
          {t('保存')}
        </Button>
      </div>
    </Card>
  );
}
