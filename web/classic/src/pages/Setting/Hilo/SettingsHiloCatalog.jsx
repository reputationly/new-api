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

import React, { useEffect, useRef, useState } from 'react';
import { Banner, Button, Card, Form, Space } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess, showWarning } from '../../../helpers';

/**
 * MiniMax Design（蒜狸小助手）客户端的模型目录。
 *
 * 这份配置决定客户端**能选哪些模型、每个模型有哪些参数、参数之间怎么互斥**。
 * 它由 `GET /api/v1/models/config` 下发，客户端重启后生效。
 *
 * ## 为什么是一个 JSON 框而不是表单
 *
 * 单条模型有二十来个字段，还带嵌套的 `params` / `paramConstraints` /
 * `inputMediaLimits`。做成表单要铺很大一片，而**改它的频率很低**——
 * 只在上线新模型或跟进官方新版本时动一次。JSON 直接可读、可整段复制，
 * 也便于和官方的真实响应做对照。
 *
 * ## 保存失败不是坏事
 *
 * 后端在保存时就校验（`setting/hilo_catalog.go` 的 `normalizeHiloCatalog`），
 * 而不是等客户端那头拒绝：客户端用 zod 校验，**失败粒度是整份目录**，
 * 一条写错会让所有模型一起消失。那时你看到的是"保存成功但客户端什么都没有"，
 * 隔着一次重启根本联系不起来。所以这里报错是**把问题提前暴露**。
 */
const SettingsHiloCatalog = (props) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({ HiloCatalog: '' });
  const refForm = useRef();

  useEffect(() => {
    const value = props.options?.HiloCatalog;
    if (value === undefined) return;
    // 服务端存的是压缩过的一行 JSON，展开成缩进的再给人看/改。
    // 解析不了就原样显示 —— 这时候更要让人看见原文，而不是一个空框。
    let pretty = value;
    try {
      pretty = JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      /* 原样 */
    }
    setInputs({ HiloCatalog: pretty });
    refForm.current?.setValues({ HiloCatalog: pretty });
  }, [props.options]);

  async function onSubmit() {
    const value = inputs.HiloCatalog ?? '';
    // 先在本地挡一道明显的语法错。**后端还会再校验一次业务规则**
    // （backend 枚举、默认值在不在选项里……），这里只是让最常见的
    // "少个逗号"当场就能看见，不用等一次往返。
    if (value.trim() !== '') {
      try {
        JSON.parse(value);
      } catch (e) {
        return showError(t('不是合法的 JSON：') + e.message);
      }
    }
    setLoading(true);
    try {
      const res = await API.put('/api/option/', {
        key: 'HiloCatalog',
        value,
      });
      if (res === undefined) return;
      if (res.data?.success === false) {
        // 后端的校验信息是可读的中文（"xxx 的 backend 不是官方支持的值"），
        // 原样透出去比包一层"保存失败"有用得多。
        return showError(res.data.message || t('保存失败，请重试'));
      }
      showSuccess(t('保存成功'));
      props.refresh?.();
    } catch (e) {
      showError(e?.response?.data?.message || t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  }

  function onResetToDefault() {
    // 空串 = 回到后端写死的出厂目录（见 `defaultHiloCatalog`）。
    // **不是"清空模型"** —— 空配置退化成没有模型的话，客户端选择器全空，
    // 而用户分不清是没配还是坏了。
    setInputs({ HiloCatalog: '' });
    refForm.current?.setValues({ HiloCatalog: '' });
    showWarning(t('已填入空值，点保存后会恢复为出厂目录'));
  }

  return (
    <Card style={{ marginTop: '10px' }}>
      <Form
        getFormApi={(api) => (refForm.current = api)}
        onValueChange={(values) => setInputs({ ...inputs, ...values })}
      >
        <Form.Section text={t('蒜狸小助手 · 模型目录')}>
          <Banner
            type='info'
            closeIcon={null}
            description={t(
              '决定客户端能选哪些模型、每个模型有哪些参数。留空则使用出厂目录。' +
                '只会报出本站真实存在渠道的模型——渠道里没有的不会出现在客户端。' +
                '改完客户端需要重启。',
            )}
            style={{ marginBottom: 12 }}
          />
          <Form.TextArea
            field='HiloCatalog'
            label={t('目录 JSON')}
            placeholder={t('留空使用出厂目录')}
            autosize={{ minRows: 16, maxRows: 40 }}
            style={{ fontFamily: 'monospace', fontSize: 12 }}
          />
          <Space>
            <Button type='primary' loading={loading} onClick={onSubmit}>
              {t('保存')}
            </Button>
            <Button theme='light' onClick={onResetToDefault}>
              {t('恢复出厂目录')}
            </Button>
          </Space>
        </Form.Section>
      </Form>
    </Card>
  );
};

export default SettingsHiloCatalog;
