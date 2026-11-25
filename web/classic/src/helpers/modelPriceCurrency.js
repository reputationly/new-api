export function getModelPricingCurrencyConfig(model) {
  if (model?.price_unavailable) return null;
  if (model?.pricing_currency !== 'USD') return { symbol: '¥', ratePerCny: 1 };
  const rate = Number(model.source_rate);
  return Number.isFinite(rate) && rate > 0
    ? { symbol: '$', ratePerCny: 1 / rate }
    : null;
}
