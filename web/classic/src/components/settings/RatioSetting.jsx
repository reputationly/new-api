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

import React, { useEffect, useState } from 'react';
import { Banner, Button, Card, Input, Space, Spin, Tabs } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import ModelPricingCombined from '../../pages/Setting/Ratio/ModelPricingCombined';
import ModelRatioNotSetEditor from '../../pages/Setting/Ratio/ModelRationNotSetEditor';
import ToolPriceSettings from '../../pages/Setting/Ratio/ToolPriceSettings';

import { API, showError, showSuccess } from '../../helpers';

const RatioSetting = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();

  let [inputs, setInputs] = useState({
    ModelPrice: '',
    ModelRatio: '',
    CacheRatio: '',
    CreateCacheRatio: '',
    CompletionRatio: '',
    ImageRatio: '',
    AudioRatio: '',
    AudioCompletionRatio: '',
  });

  const [loading, setLoading] = useState(false);
  const [enabledModels, setEnabledModels] = useState([]);
  const [savedRate, setSavedRate] = useState('');
  const [rateDraft, setRateDraft] = useState('');
  const [savingRate, setSavingRate] = useState(false);
  useEffect(() => setRateDraft(savedRate), [savedRate]);

  const saveRate = async () => {
    const rate = Number(rateDraft);
    if (!Number.isFinite(rate) || rate <= 0) {
      showError(t('请输入有效的全局模型源汇率'));
      return;
    }
    setSavingRate(true);
    try {
      const value = String(rate);
      const { data } = await API.post('/api/model_pricing/options', {
        options: { ModelSourceRate: value },
      });
      if (!data.success) {
        showError(data.message);
        return;
      }
      setSavedRate(value);
      showSuccess(t('保存成功'));
    } catch (error) {
      showError(error.message);
    } finally {
      setSavingRate(false);
    }
  };

  const getOptions = async () => {
    const res = await API.get('/api/model_pricing/options');
    const { success, message, data, enabled_models } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        if (item.value.startsWith('{') || item.value.startsWith('[')) {
          try {
            item.value = JSON.stringify(JSON.parse(item.value), null, 2);
          } catch (e) {
            // 如果后端返回的不是合法 JSON，直接展示
          }
        }
        newInputs[item.key] = item.value;
      });
      setInputs(newInputs);
      setSavedRate(newInputs.ModelSourceRate || '');
      setEnabledModels(enabled_models || []);
    } else {
      showError(message);
    }
  };

  const onRefresh = async () => {
    try {
      setLoading(true);
      await getOptions();
    } catch (error) {
      showError('刷新失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    onRefresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Spin spinning={loading} size='large'>
      <Card style={{ marginTop: '10px' }}>
        <Space wrap className='mb-3'>
          <Input
            aria-label={t('全局模型源汇率')}
            prefix={t('全局模型源汇率')}
            suffix='¥/$'
            value={rateDraft}
            onChange={setRateDraft}
            style={{ width: 260 }}
          />
          <Button loading={savingRate} onClick={saveRate}>
            {t('保存全局汇率')}
          </Button>
          <span>{t('只影响美元价格及引用 fx 的新请求，不改原价。')}</span>
        </Space>
        <Banner
          type='info'
          closeIcon={null}
          className='mb-3'
          description={
            <div className='flex flex-wrap items-center gap-2'>
              <span>
                {t(
                  '分组倍率、自动分组、跨分组规则等已迁移到「分组管理」页面。',
                )}
              </span>
              <Button
                size='small'
                theme='borderless'
                onClick={() => navigate('/console/group')}
              >
                {t('前往分组管理')}
              </Button>
            </div>
          }
        />
        <Tabs type='card' defaultActiveKey='pricing'>
          <Tabs.TabPane tab={t('模型定价设置')} itemKey='pricing'>
            <ModelPricingCombined options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('未设置价格模型')} itemKey='unset_models'>
            <ModelRatioNotSetEditor options={inputs} refresh={onRefresh} enabledModels={enabledModels} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('工具调用定价')} itemKey='tool_price'>
            <ToolPriceSettings options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
        </Tabs>
      </Card>
    </Spin>
  );
};

export default RatioSetting;
