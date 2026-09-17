import { useTranslation } from 'react-i18next'

import type { PricingModel } from '../types'

export function ModelDiscountCaption(props: { model: PricingModel }) {
  const { t } = useTranslation()
  const discount = props.model.model_discount
  if (!discount || discount.model !== props.model.model_name) return null
  let source = t('Model discount', { defaultValue: '模型折扣' })
  if (discount.source === 'user') source = t('Personal discount', { defaultValue: '个人专属' })
  if (discount.source === 'group') source = t('Customer group discount', { defaultValue: '客户组优惠' })
  const label = discount.factor === 1
    ? t('Original price', { defaultValue: '原价' })
    : t('Discount tenths', { defaultValue: '{{value}}折', value: Number((discount.factor * 10).toFixed(4)) })
  return <span className='text-primary block text-xs'>{source} · {label} · ×{discount.factor}</span>
}
