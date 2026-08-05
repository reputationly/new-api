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

import { createContext, useCallback, useContext, useEffect } from 'react';

const ThemeContext = createContext(null);
export const useTheme = () => useContext(ThemeContext);

const ActualThemeContext = createContext(null);
export const useActualTheme = () => useContext(ActualThemeContext);

const SetThemeContext = createContext(null);
export const useSetTheme = () => useContext(SetThemeContext);

// 站点只维护浅色一套皮肤：深色变体的组件覆盖长期没跟上，切过去会出现底色变黑、
// 但卡片和按钮仍是浅色前景的错配，因此顶栏的主题切换入口已下线。
//
// 注意这里不能只藏入口 —— 此前把 theme-mode 存成 dark（或 auto 且系统是深色）的老用户，
// 一旦入口消失就永远卡在深色里、再没有切回来的办法。所以主题在 Provider 层直接钉死为
// light，并清掉历史残留值；三个 context 依旧导出，消费方（如首页 iframe 主题同步）
// 拿到的恒为 'light'，无需改动。
const THEME = 'light';

export const ThemeProvider = ({ children }) => {
  useEffect(() => {
    document.body.removeAttribute('theme-mode');
    document.documentElement.classList.remove('dark');
    try {
      localStorage.removeItem('theme-mode');
    } catch {
      // 隐私模式下 localStorage 不可写，忽略即可
    }
  }, []);

  // 保留 setter 形状，避免残存调用方炸掉；调用无副作用
  const setTheme = useCallback(() => {}, []);

  return (
    <SetThemeContext.Provider value={setTheme}>
      <ActualThemeContext.Provider value={THEME}>
        <ThemeContext.Provider value={THEME}>{children}</ThemeContext.Provider>
      </ActualThemeContext.Provider>
    </SetThemeContext.Provider>
  );
};
