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
import { getCurrencyConfig } from './render';

export const getQuotaPerUnit = () => {
  const raw = parseFloat(localStorage.getItem('quota_per_unit') || '1');
  return Number.isFinite(raw) && raw > 0 ? raw : 1;
};

// ---- 积分换算（内部 quota unit ↔ 展示积分数） ----

export const getQuotaPerPoint = () => {
  const raw = parseFloat(localStorage.getItem('quota_per_point') || '684.93');
  return Number.isFinite(raw) && raw > 0 ? raw : 684.93;
};

export const isPointsEnabled = () =>
  localStorage.getItem('points_enabled') === 'true';

// quota unit -> 积分数（floor 取整，积分对用户不显示小数）。
// 1e-9 为浮点护栏：整数 quota 的相邻比值间距 ≈1/qpp 远大于该值，不会虚增，
// 只防止除法在整数边界向下抖动（如 ceil 发放后的精确往返被浮点误差打破）
export const quotaToPoints = (quota) => {
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q <= 0) return 0;
  return Math.floor(q / getQuotaPerPoint() + 1e-9);
};

// 积分数 -> quota unit（ceil 取整，用于提交给后端）。与后端 PointsToQuota 一致：
// 向上取整保证 quotaToPoints(pointsToQuota(n)) === n 精确往返，差额 <1 quota unit 让利用户
export const pointsToQuota = (points) => {
  const p = Number(points || 0);
  if (!Number.isFinite(p) || p <= 0) return 0;
  return Math.ceil(p * getQuotaPerPoint());
};

export const quotaToDisplayAmount = (quota) => {
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q === 0) return 0;
  const sign = Math.sign(q);
  const abs = Math.abs(q);
  const { type, rate } = getCurrencyConfig();
  if (type === 'TOKENS') return q;
  const usd = abs / getQuotaPerUnit();
  if (type === 'USD') return sign * usd;
  return sign * usd * (rate || 1);
};

export const displayAmountToQuota = (amount) => {
  const val = Number(amount || 0);
  if (!Number.isFinite(val) || val === 0) return 0;
  const sign = Math.sign(val);
  const abs = Math.abs(val);
  const { type, rate } = getCurrencyConfig();
  if (type === 'TOKENS') return Math.round(val);
  const usd = type === 'USD' ? abs : abs / (rate || 1);
  return sign * Math.round(usd * getQuotaPerUnit());
};
