import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Banner,
  Button,
  Input,
  Modal,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';

import { API } from '../../helpers';

const { Text } = Typography;

// 模型可见性诊断：回答「为什么这个模型在体验区/模型列表里选不到」。
//
// 存在的理由：模型不可见有五六个互相独立的原因（别名把名字声明成内部名、渠道被禁用、
// 端点类型被判成视频/图片、被体验区媒体配置占用、大小写变体、缓存未刷新），它们分布在
// DB 行、运行时缓存和前端过滤三层，而每一层都是静默失败 —— 保存渠道返回成功、库里也
// 确实有记录，但用户永远看不到。此前只能靠翻 DB 逐个猜，这个入口把那套排查压缩成一次点击。
//
// 判据全部在后端（GET /api/channel/diagnose），这里只渲染，避免两边各写一套而给出
// 与页面相反的结论。
const ModelDiagnoseModal = ({ visible, onClose, defaultModel = '' }) => {
  const { t } = useTranslation();
  const [keyword, setKeyword] = useState(defaultModel);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');

  // 每次打开都把当前选中的模型带进来：绝大多数情况下用户问的就是"我这个为什么不行"
  useEffect(() => {
    if (visible) {
      setKeyword(defaultModel || '');
      setResult(null);
      setError('');
    }
  }, [visible, defaultModel]);

  const runDiagnose = async () => {
    const model = String(keyword || '').trim();
    if (!model) {
      setError(t('请先填写模型名称'));
      return;
    }
    setLoading(true);
    setError('');
    setResult(null);
    try {
      const res = await API.get(
        `/api/channel/diagnose?model=${encodeURIComponent(model)}`,
      );
      if (res?.data?.success) {
        setResult(res.data.data);
      } else {
        setError(res?.data?.message || t('诊断失败'));
      }
    } catch (e) {
      setError(e?.response?.data?.message || t('诊断失败，请稍后重试'));
    } finally {
      setLoading(false);
    }
  };

  const renderList = (title, items, tone) =>
    items && items.length > 0 ? (
      <div className='mt-3'>
        <Text strong className='text-sm'>
          {title}
        </Text>
        <ul className='mt-1 pl-5 list-disc'>
          {items.map((item) => (
            <li key={item}>
              <Text size='small' type={tone}>
                {item}
              </Text>
            </li>
          ))}
        </ul>
      </div>
    ) : null;

  return (
    <Modal
      title={t('模型可见性诊断')}
      visible={visible}
      onCancel={onClose}
      footer={
        <div className='flex justify-end gap-2'>
          <Button onClick={onClose}>{t('关闭')}</Button>
          <Button
            theme='solid'
            type='primary'
            loading={loading}
            onClick={runDiagnose}
          >
            {t('诊断')}
          </Button>
        </div>
      }
      style={{ width: 620, maxWidth: '92vw' }}
    >
      <div className='flex gap-2'>
        <Input
          value={keyword}
          placeholder={t('输入模型名称，例如 qwen3.8-max')}
          onChange={setKeyword}
          onEnterPress={runDiagnose}
          showClear
        />
      </div>
      <Text type='tertiary' size='small' className='block mt-2'>
        {t(
          '诊断会同时检查：渠道是否禁用、名字是否被模型别名当成内部名、端点类型是否被判成非文本、是否被体验区媒体配置占用、是否存在仅大小写不同的变体。',
        )}
      </Text>

      {loading && (
        <div className='mt-4 flex justify-center'>
          <Spin />
        </div>
      )}

      {error && (
        <Banner
          className='mt-3'
          type='danger'
          closeIcon={null}
          description={error}
        />
      )}

      {result && (
        <div className='mt-3'>
          <Banner
            type={result.visible ? 'success' : 'danger'}
            closeIcon={null}
            description={
              result.visible
                ? t(
                    '这个模型在文本体验区可见（若仍未出现，请硬刷新页面清掉前端缓存）',
                  )
                : t('这个模型在文本体验区不可见，原因见下')
            }
          />

          {renderList(t('阻断原因'), result.blockers, 'danger')}
          {renderList(t('提示（不影响可用性）'), result.warnings, 'warning')}

          {result.abilities?.length > 0 && (
            <div className='mt-3'>
              <Text strong className='text-sm'>
                {t('渠道与分组')}
              </Text>
              <div className='mt-1 flex flex-col gap-1'>
                {result.abilities.map((a) => (
                  <div
                    key={`${a.channel_id}-${a.group}`}
                    className='flex items-center gap-2 flex-wrap'
                  >
                    <Text size='small'>
                      #{a.channel_id} {a.channel_name || t('渠道已删除')}
                    </Text>
                    <Tag size='small' color='blue'>
                      {a.group}
                    </Tag>
                    <Tag size='small' color={a.enabled ? 'green' : 'grey'}>
                      {a.enabled ? t('启用') : t('禁用')}
                    </Tag>
                    {a.channel_status !== 1 && (
                      <Text size='small' type='danger'>
                        {t('渠道状态异常')}: {a.channel_status}
                      </Text>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}

          {result.hidden_by?.length > 0 && (
            <div className='mt-3'>
              <Text strong className='text-sm'>
                {t('被声明为内部名的位置')}
              </Text>
              <div className='mt-1 flex flex-col gap-1'>
                {result.hidden_by.map((d) => (
                  <Text key={`${d.channel_id}-${d.public_name}`} size='small'>
                    #{d.channel_id} {d.channel_name} → {d.public_name}
                  </Text>
                ))}
              </div>
            </div>
          )}

          {result.endpoint_types?.length > 0 && (
            <div className='mt-3 flex items-center gap-2 flex-wrap'>
              <Text strong className='text-sm'>
                {t('端点类型')}
              </Text>
              {result.endpoint_types.map((et) => (
                <Tag key={et} size='small'>
                  {et}
                </Tag>
              ))}
            </div>
          )}

          {result.case_variants?.length > 1 && (
            <div className='mt-3'>
              <Text strong className='text-sm'>
                {t('仅大小写不同的变体')}
              </Text>
              <div className='mt-1 flex flex-col gap-1'>
                {result.case_variants.map((v) => (
                  <Text key={v.model} size='small'>
                    {v.model} · {v.enabled ? t('有启用渠道') : t('全部禁用')} ·{' '}
                    {t('渠道')} {v.channel_ids?.join(', ') || '-'}
                  </Text>
                ))}
              </div>
            </div>
          )}

          {result.media_configs?.length > 0 && (
            <div className='mt-3'>
              <Text strong className='text-sm'>
                {t('占用该模型的体验区配置')}
              </Text>
              <div className='mt-1 flex gap-2 flex-wrap'>
                {result.media_configs.map((k) => (
                  <Tag key={k} size='small' color='orange'>
                    {k}
                  </Tag>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
};

export default ModelDiagnoseModal;
