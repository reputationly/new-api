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
import { Spin } from '@douyinfe/semi-ui';
import SettingsHiloCatalog from '../../pages/Setting/Hilo/SettingsHiloCatalog';
import { API, showError } from '../../helpers';

/** 蒜狸小助手（MiniMax Design 客户端）相关的设置。 */
const HiloSetting = () => {
  const [inputs, setInputs] = useState({ HiloCatalog: '' });
  const [loading, setLoading] = useState(false);

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (!success) return showError(message);
    const next = {};
    data.forEach((item) => {
      next[item.key] = item.value;
    });
    setInputs(next);
  };

  async function onRefresh() {
    try {
      setLoading(true);
      await getOptions();
    } catch {
      showError('刷新失败');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    onRefresh();
  }, []);

  return (
    <Spin spinning={loading} size='large'>
      <SettingsHiloCatalog options={inputs} refresh={onRefresh} />
    </Spin>
  );
};

export default HiloSetting;
