import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { getGroups, searchUsers } from '@/features/users/api'
import { useAuthStore } from '@/stores/auth-store'

import { getSystemOptions, updateSystemOption } from '../api'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import { addDiscountRule, discountRows, parseDiscountRules, removeDiscountRule, type DiscountRow, type ModelDiscountRules } from './model-discount-rules'

export function ModelDiscountRulesCard() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [reload, setReload] = useState(0)
  const query = useQuery({
    queryKey: ['model-discount-rules', userId],
    queryFn: async () => {
      const response = await getSystemOptions()
      if (!response.success || !Array.isArray(response.data)) throw new Error(response.message || t('Failed to load settings'))
      return parseDiscountRules(response.data.find((option) => option.key === 'ModelDiscountRules')?.value ?? '{}')
    },
    retry: false,
  })
  return (
    <Card>
      <CardHeader><CardTitle>{t('Model discounts', { defaultValue: '逐模型客户折扣' })}</CardTitle></CardHeader>
      <CardContent>
        {query.isPending && <p role='status'>{t('Loading...')}</p>}
        {query.isError && <div role='alert'>{t('Failed to load settings')} <Button variant='outline' onClick={() => void query.refetch()}>{t('Retry')}</Button></div>}
        {query.data && !query.isError && <DiscountEditor key={`${userId}:${reload}`} initialRules={query.data} onReload={async () => { const result = await query.refetch(); if (result.isSuccess) setReload(value => value + 1) }} />}
      </CardContent>
    </Card>
  )
}

function DiscountEditor(props: { initialRules: ModelDiscountRules; onReload: () => Promise<void> }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [rules, setRules] = useState(props.initialRules)
  const [saved, setSaved] = useState(JSON.stringify(props.initialRules))
  const [kind, setKind] = useState<DiscountRow['kind']>('groups')
  const [owner, setOwner] = useState('')
  const [model, setModel] = useState('')
  const [percent, setPercent] = useState('100')
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<DiscountRow | null>(null)
  const dirty = JSON.stringify(rules) !== saved
  const groups = useQuery({
    queryKey: ['model-discount-groups', userId],
    queryFn: async () => {
      const response = await getGroups()
      if (!response.success || !Array.isArray(response.data)) throw new Error(response.message || 'Failed to load groups')
      return response.data
    },
  })
  const models = useQuery({
    queryKey: ['model-discount-models', userId],
    queryFn: async () => {
      const { data: response } = await api.get<{ success: boolean; data: string[]; message?: string }>('/api/option/model_discount_models')
      if (!response.success || !Array.isArray(response.data)) throw new Error(response.message || 'Failed to load models')
      return [...new Set(response.data)].sort()
    },
  })
  const users = useQuery({
    queryKey: ['model-discount-users', userId, search, page],
    enabled: kind === 'users',
    queryFn: async () => {
      const response = await searchUsers({ keyword: search, p: page, page_size: 20 })
      if (!response.success || !response.data || !Array.isArray(response.data.items)) throw new Error(response.message || 'Failed to load users')
      return response.data
    },
  })
  const save = useMutation({
    mutationFn: async (next: ModelDiscountRules) => {
      const value = JSON.stringify(next)
      const response = await updateSystemOption({ key: 'ModelDiscountRules', value, expected_value: saved })
      if (!response.success) throw new Error(response.message || t('Failed to update setting'))
      return value
    },
    onSuccess: (value) => {
      setSaved(value)
      queryClient.setQueryData(['model-discount-rules', userId], JSON.parse(value))
      void queryClient.invalidateQueries({ queryKey: ['pricing'] })
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
      toast.success(t('Setting updated successfully'))
    },
    onError: (failure: Error) => setError(failure.message),
  })
  const ownerOptions = kind === 'groups'
    ? (groups.data ?? []).map((value) => ({ value, label: value }))
    : (users.data?.items ?? []).map((user) => ({ value: String(user.id), label: `${user.display_name || user.username} · ${user.username} (#${user.id})` }))
  const catalogFailed = groups.isError || models.isError || (kind === 'users' && users.isError)
  const catalogLoading = groups.isPending || models.isPending || (kind === 'users' && users.isPending)
  const factor = Number(percent) / 100
  const validPercent = percent.trim() !== '' && Number.isFinite(factor) && factor >= 0 && factor <= 1
  const discountText = (value: number) => value === 1
    ? t('Original price', { defaultValue: '原价' })
    : t('Discount tenths', { defaultValue: '{{value}}折', value: Number((value * 10).toFixed(4)) })

  function addRule() {
    setError('')
    if (!validPercent || !ownerOptions.some((option) => option.value === owner) || !models.data?.includes(model)) return
    try {
      setRules(addDiscountRule(rules, { kind, owner, model, factor }))
      setModel('')
      setPercent('100')
    } catch {
      setError(t('Duplicate model discount', { defaultValue: '该客户与模型已有规则，请先明确删除原行后再新增。' }))
    }
  }

  return <div className='space-y-4'>
    <FormNavigationGuard when={dirty} />
    <p className='text-muted-foreground text-sm'>{t('Model discount explanation', { defaultValue: '未设置规则时折扣为 1（原价）。个人规则优先于客户组规则，不叠乘；折扣乘以现有有效组倍率，仅匹配精确模型名。' })}</p>
    <fieldset disabled={save.isPending} className='space-y-3'>
      <div className='grid gap-3 sm:grid-cols-2'>
        <Combobox openOnFocus={false} aria-label={t('Rule type', { defaultValue: '规则类型' })} value={kind} options={[{ value: 'groups', label: t('Customer group', { defaultValue: '客户组' }) }, { value: 'users', label: t('User') }]} onValueChange={(value) => { setKind(value === 'users' ? 'users' : 'groups'); setOwner('') }} />
        {kind === 'users' && <div className='flex gap-2'>
          <Input aria-label={t('Search users', { defaultValue: '搜索用户' })} value={keyword} onChange={(event) => setKeyword(event.target.value)} />
          <Button variant='outline' onClick={() => { setSearch(keyword); setPage(1); setOwner('') }}>{t('Search')}</Button>
        </div>}
        <Combobox openOnFocus={false} aria-label={t('Discount owner', { defaultValue: '折扣客户' })} placeholder={t('Select', { defaultValue: '请选择' })} options={ownerOptions} value={owner} onValueChange={(value) => setOwner(value ?? '')} disabled={catalogLoading || catalogFailed} />
        <Combobox openOnFocus={false} aria-label={t('Exact model', { defaultValue: '精确模型' })} placeholder={t('Exact model', { defaultValue: '精确模型' })} options={(models.data ?? []).map((value) => ({ value, label: value }))} value={model} onValueChange={(value) => setModel(value ?? '')} disabled={catalogLoading || catalogFailed} />
      </div>
      {kind === 'users' && users.data && <div className='flex items-center gap-2 text-sm'>
        <Button variant='outline' disabled={page <= 1} onClick={() => { setPage(page - 1); setOwner('') }}>{t('Previous')}</Button>
        <span>{page} / {Math.max(1, Math.ceil(users.data.total / 20))} · {users.data.total}</span>
        <Button variant='outline' disabled={page * 20 >= users.data.total} onClick={() => { setPage(page + 1); setOwner('') }}>{t('Next')}</Button>
      </div>}
      {catalogLoading && <p role='status'>{t('Loading...')}</p>}
      {catalogFailed && <p role='alert'>{t('Discount catalog failed', { defaultValue: '客户或模型目录加载失败，无法新增；请重试。' })} <Button variant='outline' onClick={() => { void groups.refetch(); void models.refetch(); if (kind === 'users') void users.refetch() }}>{t('Retry')}</Button></p>}
      <div className='flex flex-wrap items-center gap-3'>
        <label htmlFor='model-discount-percent'>{t('Pay percentage', { defaultValue: '实付比例（0–100%）' })}</label>
        <Input id='model-discount-percent' className='w-28' type='number' min={0} max={100} step='any' value={percent} onChange={(event) => setPercent(event.target.value)} aria-invalid={!validPercent} />
        <span>{validPercent ? `${discountText(factor)} · ×${factor}` : t('Enter 0 to 100', { defaultValue: '请输入 0–100' })}</span>
        <Button onClick={addRule} disabled={!owner || !model || !validPercent || catalogLoading || catalogFailed}>{t('Add rule', { defaultValue: '新增规则' })}</Button>
      </div>
      {discountRows(rules).length === 0 && <p className='text-muted-foreground'>{t('No model discounts', { defaultValue: '暂无折扣规则，所有客户保持原价（×1）。' })}</p>}
      <div className='space-y-2'>
        {discountRows(rules).map((row) => <div key={JSON.stringify([row.kind, row.owner, row.model])} className='flex flex-wrap items-center gap-3 rounded-md border p-3'>
          <span>{row.kind === 'users' ? t('User') : t('Customer group', { defaultValue: '客户组' })} · {row.owner}</span>
          <code className='min-w-0 flex-1 break-all'>{row.model}</code>
          <span>{Number((row.factor * 100).toFixed(4))}% · {discountText(row.factor)}</span>
          <Button variant='outline' onClick={() => setDeleting(row)}>{t('Delete')}</Button>
        </div>)}
      </div>
      {error && <div role='alert' className='space-y-2 text-destructive'><p>{error}</p><Button variant='outline' onClick={() => { if (window.confirm('重新加载将放弃未保存的草稿，读取服务器最新规则。继续？')) void props.onReload() }}>重新加载最新规则</Button></div>}
      <div className='flex items-center gap-3'>
        <Button onClick={() => { setError(''); save.mutate(rules) }} disabled={!dirty || save.isPending}>{save.isPending ? t('Saving...') : t('Save all rules', { defaultValue: '保存全部规则' })}</Button>
        {dirty && <span role='status' className='text-muted-foreground'>{t('Unsaved changes')}</span>}
      </div>
    </fieldset>
    <ConfirmDialog open={deleting !== null} onOpenChange={(open) => { if (!open) setDeleting(null) }} title={t('Delete rule', { defaultValue: '删除此规则？' })} desc={deleting ? `${deleting.kind === 'users' ? t('User') : t('Customer group', { defaultValue: '客户组' })} ${deleting.owner} · ${deleting.model} · ${discountText(deleting.factor)}` : ''} confirmText={t('Delete')} destructive handleConfirm={() => { if (deleting) setRules(removeDiscountRule(rules, deleting)); setDeleting(null) }} />
  </div>
}
