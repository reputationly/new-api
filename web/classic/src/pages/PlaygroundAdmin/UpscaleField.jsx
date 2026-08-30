import React from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Select, Typography } from '@douyinfe/semi-ui';
import { Plus, Trash2 } from 'lucide-react';
import {
  getSizesForVideoModel,
  videoSizeShortEdge,
} from '../../constants/videoPlayground.constants';

const { Text } = Typography;

// 超分档位配置（模型级）。一行 = 一条「用哪个超分模型、放大到哪一档、从哪一档起步」，
// 体验区据此在尺寸下拉里多出一个带标识的档位。
//
// 三个下拉的候选各有来源：
//   超分模型 —— 列出全部视频模型，**不做模型名校验**。task_type 是显式下发的
//     （adaptor.go 的四级解析里 metadata.task_type 排第一、直接短路名字推断），配错的
//     后果是门面明确拒绝，而不是静默按 t2v 跑掉。
//   目标档位 —— 所选超分模型声明的 sizes（tab 级 / 模型级 / 分类默认值三层取并集，与
//     体验区同口径）。它是引擎侧部署 config 的目标尺寸（这份是部署事实，new-api 无从
//     校验），运营给超分模型登记一次即可。
//   起步档位 —— 本模型已配的原生档，且只列**小于目标**的那些；必选，不再有「自动」。
//     自动那一档看不出最终取值、配完常常整档不出现，改成运营在这里点定一档。
//
// 倍率不出现在这里：SR **按分辨率档位出片**，倍率只是个够不着的上限。引擎取
// min(源面积×倍率, config target 面积)，前端固定发一个足够大的值让它恒取右项即可，
// 起步档差异自动抹平。让运营填倍率只会填错——同一个「到 1080」，480P 起步要 2.28、
// 720P 起步要 1.5、768P 起步要 1.4，而且算错了不报错、只是悄悄掉档。
// srModelUnused：该模型走「高分辨率档用纯放大」，超分模型这一格不会被调用。
//
// 置灰而不是隐藏，更不是允许清空：规则行缺 model 会在保存时被 normalizeUpscaleList
// 整行丢弃，而档位（1080P / 2K）正是由这些规则**定义**的 —— 清掉等于把用户的高分辨率
// 档位一起删了，且症状是「体验区下拉里那两档莫名消失」，不会有任何报错。
// 所以它必须留着一个合法值，只是不再生效；界面要把这件事说出来，否则下一个人一定会
// 问「勾了纯放大为什么还要选超分模型」。
const UpscaleField = ({
  value,
  onChange,
  models,
  defaults,
  nativeSizes,
  srModelUnused,
}) => {
  const { t } = useTranslation();
  const rules = Array.isArray(value) ? value : [];
  const modelNames = Object.keys(models || {}).sort();

  const patch = (idx, key, v) =>
    onChange(rules.map((r, i) => (i === idx ? { ...r, [key]: v } : r)));
  const remove = (idx) => {
    const next = rules.filter((_, i) => i !== idx);
    onChange(next.length ? next : undefined);
  };
  const add = () => onChange([...rules, { model: '', to: '', from: '' }]);

  return (
    <div className='w-full'>
      {rules.map((r, i) => {
        // 目标档位要覆盖 sizes 的全部三个来源：tab 级、模型级、分类默认值。只读模型级
        // 会让「sizes 落在别处」的超分模型在这里显示空下拉，运营根本配不出规则，而体验区
        // 运行时用 getSizesForVideoModel 照样取得到值 —— 两边判据分叉。
        // 超分模型通常不挂任何 tab（sizes 落模型级、入口在 CategoryPanel 的孤儿字段区），
        // 但没有任何机制保证它不被挂进 tab，所以取并集。
        const srSizes = Array.from(
          new Set([
            ...Object.values(models?.[r.model]?.tabs || {}).flatMap(
              (e) => e?.sizes || [],
            ),
            ...getSizesForVideoModel(
              { models, default: defaults },
              r.model,
              '',
            ),
          ]),
        );
        const targetEdge = videoSizeShortEdge(r.to);
        // 只有比目标小的档位才是合法起步档：目标档本身已在已配档位里时，体验区会让
        // 原生档优先、这条规则整条让位（buildVideoSizeChoices 的 taken 判据）。
        const fromChoices = (nativeSizes || []).filter((s) => {
          const edge = videoSizeShortEdge(s);
          return edge > 0 && (!targetEdge || edge < targetEdge);
        });
        return (
          <div key={i} className='flex flex-wrap items-center gap-2 mb-2'>
            <Text type='tertiary' size='small'>
              {t('超分模型')}
            </Text>
            <Select
              size='small'
              filter
              disabled={srModelUnused}
              style={{ minWidth: 200 }}
              placeholder={t('选择超分模型')}
              value={r.model || ''}
              optionList={modelNames.map((m) => ({ label: m, value: m }))}
              onChange={(v) => patch(i, 'model', v || '')}
            />
            {srModelUnused && (
              <Text type='tertiary' size='small'>
                {t('（本模型不调用它：已启用纯放大）')}
              </Text>
            )}
            <Text type='tertiary' size='small'>
              {t('超分至')}
            </Text>
            <Select
              size='small'
              style={{ minWidth: 130 }}
              placeholder={r.model ? t('该模型未登记档位') : t('先选超分模型')}
              value={r.to || ''}
              optionList={srSizes.map((s) => ({ label: s, value: s }))}
              onChange={(v) => patch(i, 'to', v || '')}
            />
            <Text type='tertiary' size='small'>
              {t('起步分辨率')}
            </Text>
            <Select
              size='small'
              style={{ minWidth: 190 }}
              placeholder={
                !r.to
                  ? t('先选目标档位')
                  : fromChoices.length
                    ? t('选择起步档位')
                    : t('无更低的可用档位')
              }
              value={r.from || ''}
              optionList={fromChoices.map((s) => ({ label: s, value: s }))}
              onChange={(v) => patch(i, 'from', v || '')}
            />
            <Button
              size='small'
              theme='borderless'
              type='danger'
              icon={<Trash2 size={14} />}
              onClick={() => remove(i)}
            />
          </div>
        );
      })}
      <Button
        size='small'
        theme='borderless'
        icon={<Plus size={14} />}
        onClick={add}
      >
        {t('添加超分档位')}
      </Button>
    </div>
  );
};

export default UpscaleField;
