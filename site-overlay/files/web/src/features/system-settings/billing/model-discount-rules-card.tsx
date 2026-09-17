import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  AlertCircle,
  Check,
  ChevronLeft,
  ChevronRight,
  Percent,
  Plus,
  RefreshCw,
  Search,
  Sparkles,
  Tag,
  Trash2,
  User,
  Users,
} from 'lucide-react'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { getGroups, searchUsers } from '@/features/users/api'
import { useAuthStore } from '@/stores/auth-store'

import { getSystemOptions, updateSystemOption } from '../api'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import {
  addDiscountRule,
  discountRows,
  parseDiscountRules,
  removeDiscountRule,
  type DiscountRow,
  type ModelDiscountRules,
} from './model-discount-rules'

export function ModelDiscountRulesCard() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [reload, setReload] = useState(0)

  const query = useQuery({
    queryKey: ['model-discount-rules', userId],
    queryFn: async () => {
      const response = await getSystemOptions()
      if (!response.success || !Array.isArray(response.data)) {
        throw new Error(response.message || t('Failed to load settings'))
      }
      return parseDiscountRules(
        response.data.find((option) => option.key === 'ModelDiscountRules')?.value ?? '{}'
      )
    },
    retry: false,
  })

  return (
    <Card className='border-border/80 shadow-xs'>
      <CardHeader className='pb-4'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='space-y-1'>
            <div className='flex items-center gap-2'>
              <div className='bg-primary/10 text-primary flex size-8 items-center justify-center rounded-lg ring-1 ring-primary/20'>
                <Percent className='size-4' />
              </div>
              <CardTitle className='text-base font-bold text-stone-900 dark:text-stone-100'>
                {t('Model discounts', { defaultValue: '逐模型客户折扣' })}
              </CardTitle>
            </div>
            <CardDescription className='text-xs text-stone-500 dark:text-stone-400'>
              {t(
                'Model discount explanation',
                {
                  defaultValue:
                    '未设置规则时折扣为 1（原价）。个人规则优先于客户组规则，不叠乘；折扣乘以现有有效组倍率，仅匹配精确模型名。',
                }
              )}
            </CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {query.isPending && (
          <div className='flex items-center justify-center py-12 text-sm text-stone-500'>
            <RefreshCw className='mr-2 size-4 animate-spin text-amber-500' />
            <p role='status'>{t('Loading...')}</p>
          </div>
        )}
        {query.isError && (
          <div role='alert' className='flex flex-col items-center justify-center gap-3 py-8 text-center text-sm text-destructive'>
            <AlertCircle className='size-6 text-destructive/80' />
            <p>{t('Failed to load settings')}</p>
            <Button variant='outline' size='sm' onClick={() => void query.refetch()}>
              {t('Retry')}
            </Button>
          </div>
        )}
        {query.data && !query.isError && (
          <DiscountEditor
            key={`${userId}:${reload}`}
            initialRules={query.data}
            onReload={async () => {
              const result = await query.refetch()
              if (result.isSuccess) setReload((value) => value + 1)
            }}
          />
        )}
      </CardContent>
    </Card>
  )
}

function DiscountEditor(props: {
  initialRules: ModelDiscountRules
  onReload: () => Promise<void>
}) {
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
  const [tableFilter, setTableFilter] = useState('')
  const [deleting, setDeleting] = useState<DiscountRow | null>(null)

  const dirty = JSON.stringify(rules) !== saved

  const groups = useQuery({
    queryKey: ['model-discount-groups', userId],
    queryFn: async () => {
      const response = await getGroups()
      if (!response.success || !Array.isArray(response.data)) {
        throw new Error(response.message || 'Failed to load groups')
      }
      return response.data
    },
  })

  const models = useQuery({
    queryKey: ['model-discount-models', userId],
    queryFn: async () => {
      const { data: response } = await api.get<{
        success: boolean
        data: string[]
        message?: string
      }>('/api/option/model_discount_models')
      if (!response.success || !Array.isArray(response.data)) {
        throw new Error(response.message || 'Failed to load models')
      }
      return [...new Set(response.data)].sort()
    },
  })

  const users = useQuery({
    queryKey: ['model-discount-users', userId, search, page],
    enabled: kind === 'users',
    queryFn: async () => {
      const response = await searchUsers({ keyword: search, p: page, page_size: 20 })
      if (!response.success || !response.data || !Array.isArray(response.data.items)) {
        throw new Error(response.message || 'Failed to load users')
      }
      return response.data
    },
  })

  const save = useMutation({
    mutationFn: async (next: ModelDiscountRules) => {
      const value = JSON.stringify(next)
      const response = await updateSystemOption({
        key: 'ModelDiscountRules',
        value,
        expected_value: saved,
      })
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
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

  const ownerOptions = useMemo(() => {
    if (kind === 'groups') {
      return (groups.data ?? []).map((value) => ({ value, label: value }))
    }
    return (users.data?.items ?? []).map((u) => ({
      value: String(u.id),
      label: `${u.display_name || u.username} · ${u.username} (#${u.id})`,
    }))
  }, [groups.data, kind, users.data?.items])

  const catalogFailed =
    groups.isError || models.isError || (kind === 'users' && users.isError)
  const catalogLoading =
    groups.isPending || models.isPending || (kind === 'users' && users.isPending)

  const factor = Number(percent) / 100
  const validPercent =
    percent.trim() !== '' && Number.isFinite(factor) && factor >= 0 && factor <= 1

  const discountText = (value: number) =>
    value === 1
      ? t('Original price', { defaultValue: '原价' })
      : t('Discount tenths', {
          defaultValue: '{{value}}折',
          value: Number((value * 10).toFixed(4)),
        })

  const discountBadgeVariant = useMemo((): 'outline' | 'secondary' | 'default' => {
    if (factor === 1) return 'outline'
    if (factor === 0) return 'secondary'
    return 'default'
  }, [factor])

  function addRule() {
    setError('')
    if (
      !validPercent ||
      !ownerOptions.some((option) => option.value === owner) ||
      !models.data?.includes(model)
    ) {
      return
    }
    try {
      setRules(addDiscountRule(rules, { kind, owner, model, factor }))
      setModel('')
      setPercent('100')
    } catch {
      setError(
        t('Duplicate model discount', {
          defaultValue: '该客户与模型已有规则，请先明确删除原行后再新增。',
        })
      )
    }
  }

  const allRows = useMemo(() => discountRows(rules), [rules])
  const filteredRows = useMemo(() => {
    const kw = tableFilter.trim().toLowerCase()
    if (!kw) return allRows
    return allRows.filter(
      (r) =>
        r.owner.toLowerCase().includes(kw) ||
        r.model.toLowerCase().includes(kw) ||
        (r.kind === 'users' ? '用户' : '客户组').includes(kw)
    )
  }, [allRows, tableFilter])

  const PRESET_DISCOUNTS = [
    { label: '95折', value: '95' },
    { label: '9折', value: '90' },
    { label: '85折', value: '85' },
    { label: '8折', value: '80' },
    { label: '7折', value: '70' },
    { label: '5折', value: '50' },
    { label: '免费 (0折)', value: '0' },
    { label: '原价 (100%)', value: '100' },
  ]

  return (
    <div className='space-y-6'>
      <FormNavigationGuard when={dirty} />

      {/* 规则配置操作面板 */}
      <fieldset
        disabled={save.isPending}
        className='rounded-xl border border-stone-200/80 bg-stone-50/50 p-4.5 dark:border-stone-800 dark:bg-stone-900/40 space-y-4'
      >
        <div className='flex items-center justify-between border-b border-stone-200/70 pb-3 dark:border-stone-800'>
          <div className='flex items-center gap-2 text-xs font-bold text-stone-800 dark:text-stone-200'>
            <Plus className='size-4 text-emerald-600 dark:text-emerald-400' />
            <span>新增专属折扣配置</span>
          </div>
          <div className='flex items-center gap-2'>
            <span className='text-[11px] text-stone-400'>
              已配置 {allRows.length} 条有效规则
            </span>
          </div>
        </div>

        <div className='grid gap-4 md:grid-cols-3'>
          {/* 规则类型 */}
          <div className='space-y-1.5'>
            <Label className='text-xs font-medium text-stone-700 dark:text-stone-300'>
              {t('Rule type', { defaultValue: '规则类型' })}
            </Label>
            <Combobox
              openOnFocus={false}
              aria-label={t('Rule type', { defaultValue: '规则类型' })}
              value={kind}
              options={[
                { value: 'groups', label: t('Customer group', { defaultValue: '客户组' }) },
                { value: 'users', label: t('User') },
              ]}
              onValueChange={(value) => {
                setKind(value === 'users' ? 'users' : 'groups')
                setOwner('')
              }}
            />
          </div>

          {/* 目标客户 / 客户组 */}
          <div className='space-y-1.5'>
            <div className='flex items-center justify-between'>
              <Label className='text-xs font-medium text-stone-700 dark:text-stone-300'>
                {t('Discount owner', { defaultValue: '折扣客户' })}
              </Label>
              {kind === 'users' && users.data && (
                <span className='text-[10px] text-stone-400'>
                  第 {page} / {Math.max(1, Math.ceil(users.data.total / 20))} 页 · 共 {users.data.total} 人
                </span>
              )}
            </div>

            {kind === 'users' ? (
              <div className='space-y-2'>
                <div className='flex gap-1.5'>
                  <Input
                    className='h-8 text-xs'
                    placeholder={t('Search users', { defaultValue: '搜索用户...' })}
                    aria-label={t('Search users', { defaultValue: '搜索用户' })}
                    value={keyword}
                    onChange={(event) => setKeyword(event.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        setSearch(keyword)
                        setPage(1)
                        setOwner('')
                      }
                    }}
                  />
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    className='h-8 shrink-0 px-2.5 text-xs'
                    onClick={() => {
                      setSearch(keyword)
                      setPage(1)
                      setOwner('')
                    }}
                  >
                    <Search className='size-3 mr-1 text-stone-400' />
                    {t('Search')}
                  </Button>
                </div>
                <Combobox
                  openOnFocus={false}
                  aria-label={t('Discount owner', { defaultValue: '折扣客户' })}
                  placeholder={t('Select', { defaultValue: '请选择用户' })}
                  options={ownerOptions}
                  value={owner}
                  onValueChange={(value) => setOwner(value ?? '')}
                  disabled={catalogLoading || catalogFailed}
                />
                {users.data && users.data.total > 20 && (
                  <div className='flex items-center justify-end gap-1.5 pt-0.5 text-xs'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      className='h-6 px-2 text-[11px]'
                      disabled={page <= 1}
                      onClick={() => {
                        setPage(page - 1)
                        setOwner('')
                      }}
                    >
                      <ChevronLeft className='size-3 mr-0.5' />
                      {t('Previous')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      className='h-6 px-2 text-[11px]'
                      disabled={page * 20 >= users.data.total}
                      onClick={() => {
                        setPage(page + 1)
                        setOwner('')
                      }}
                    >
                      {t('Next')}
                      <ChevronRight className='size-3 ml-0.5' />
                    </Button>
                  </div>
                )}
              </div>
            ) : (
              <Combobox
                openOnFocus={false}
                aria-label={t('Discount owner', { defaultValue: '折扣客户' })}
                placeholder={t('Select', { defaultValue: '请选择客户组' })}
                options={ownerOptions}
                value={owner}
                onValueChange={(value) => setOwner(value ?? '')}
                disabled={catalogLoading || catalogFailed}
              />
            )}
          </div>

          {/* 精确模型选择 */}
          <div className='space-y-1.5'>
            <Label className='text-xs font-medium text-stone-700 dark:text-stone-300'>
              {t('Exact model', { defaultValue: '精确模型' })}
            </Label>
            <Combobox
              openOnFocus={false}
              aria-label={t('Exact model', { defaultValue: '精确模型' })}
              placeholder={t('Exact model', { defaultValue: '选择目标模型' })}
              options={(models.data ?? []).map((value) => ({ value, label: value }))}
              value={model}
              onValueChange={(value) => setModel(value ?? '')}
              disabled={catalogLoading || catalogFailed}
            />
          </div>
        </div>

        {/* 折扣比率与快捷预设 */}
        <div className='rounded-lg border border-stone-200/60 bg-white/70 p-3.5 dark:border-stone-800 dark:bg-stone-900/60 space-y-2.5'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div className='flex items-center gap-2'>
              <Label
                htmlFor='model-discount-percent'
                className='text-xs font-medium text-stone-700 dark:text-stone-300'
              >
                {t('Pay percentage', { defaultValue: '实付比例（0–100%）' })}
              </Label>
              <span className='text-[11px] text-stone-400'>输入客户实际支付的百分比，例如 80 即 8 折</span>
            </div>
            {validPercent && (
              <Badge
                variant={discountBadgeVariant}
                className='font-mono text-xs font-bold'
              >
                {discountText(factor)} · ×{factor}
              </Badge>
            )}
          </div>

          <div className='flex flex-wrap items-center gap-3'>
            <div className='relative w-32'>
              <Input
                id='model-discount-percent'
                className='h-9 font-mono pr-7 text-sm font-bold'
                type='number'
                min={0}
                max={100}
                step='any'
                value={percent}
                onChange={(event) => setPercent(event.target.value)}
                aria-invalid={!validPercent}
              />
              <span className='text-stone-400 pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-xs font-semibold'>
                %
              </span>
            </div>

            {/* 快捷折扣芯片 */}
            <div className='flex flex-wrap items-center gap-1.5'>
              {PRESET_DISCOUNTS.map((preset) => (
                <button
                  key={preset.value}
                  type='button'
                  onClick={() => setPercent(preset.value)}
                  className={cn(
                    'cursor-pointer rounded-md border px-2 py-1 text-xs font-medium transition-colors',
                    percent === preset.value
                      ? 'border-emerald-500/50 bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300 dark:border-emerald-500/30'
                      : 'border-stone-200 bg-white text-stone-600 hover:bg-stone-100 dark:border-stone-700 dark:bg-stone-800 dark:text-stone-300 dark:hover:bg-stone-700'
                  )}
                >
                  {preset.label}
                </button>
              ))}
            </div>

            <div className='ml-auto'>
              <Button
                type='button'
                onClick={addRule}
                disabled={!owner || !model || !validPercent || catalogLoading || catalogFailed}
                className='h-9 gap-1.5 text-xs font-semibold'
              >
                <Plus className='size-3.5' />
                {t('Add rule', { defaultValue: '新增规则' })}
              </Button>
            </div>
          </div>
        </div>

        {catalogLoading && (
          <p role='status' className='text-xs text-stone-500'>
            {t('Loading...')}
          </p>
        )}
        {catalogFailed && (
          <Alert variant='destructive' className='py-2 text-xs'>
            <AlertCircle className='size-4' />
            <AlertDescription className='flex items-center justify-between'>
              <span>
                {t('Discount catalog failed', {
                  defaultValue: '客户或模型目录加载失败，无法新增；请重试。',
                })}
              </span>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-6 text-xs'
                onClick={() => {
                  void groups.refetch()
                  void models.refetch()
                  if (kind === 'users') void users.refetch()
                }}
              >
                {t('Retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
      </fieldset>

      {/* 规则清单展示 */}
      <div className='space-y-3'>
        <div className='flex flex-wrap items-center justify-between gap-2 px-0.5'>
          <div className='flex items-center gap-2'>
            <Tag className='size-4 text-stone-500' />
            <span className='text-sm font-bold text-stone-900 dark:text-stone-100'>
              已配置规则清单
            </span>
          </div>

          {allRows.length > 3 && (
            <div className='w-48'>
              <Input
                placeholder='筛选规则或模型...'
                value={tableFilter}
                onChange={(e) => setTableFilter(e.target.value)}
                className='h-8 text-xs'
              />
            </div>
          )}
        </div>

        {allRows.length === 0 ? (
          <div className='flex flex-col items-center justify-center rounded-xl border border-dashed border-stone-200/90 bg-stone-50/40 py-10 text-center dark:border-stone-800 dark:bg-stone-900/20'>
            <div className='flex size-10 items-center justify-center rounded-full bg-stone-100 text-stone-400 dark:bg-stone-800 dark:text-stone-500 mb-2.5'>
              <Sparkles className='size-5' />
            </div>
            <p className='text-sm font-medium text-stone-600 dark:text-stone-400'>
              {t('No model discounts', {
                defaultValue: '暂无折扣规则，所有客户保持原价（×1）。',
              })}
            </p>
            <p className='mt-0.5 text-xs text-stone-400'>
              如需为特定用户或大客户群定制单模型价格，请在上方选择并添加
            </p>
          </div>
        ) : (
          <div className='overflow-hidden rounded-xl border border-stone-200/80 bg-white dark:border-stone-800 dark:bg-stone-900 shadow-2xs'>
            <Table>
              <TableHeader>
                <TableRow className='bg-stone-50/70 hover:bg-stone-50/70 dark:bg-stone-800/50'>
                  <TableHead className='w-28 text-xs font-semibold'>适用类型</TableHead>
                  <TableHead className='min-w-[140px] text-xs font-semibold'>客户主体 / 目标</TableHead>
                  <TableHead className='min-w-[200px] text-xs font-semibold'>指定模型</TableHead>
                  <TableHead className='w-36 text-right text-xs font-semibold'>实付比例 / 折扣</TableHead>
                  <TableHead className='w-20 text-right text-xs font-semibold'>操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredRows.map((row) => {
                  const isUser = row.kind === 'users'
                  return (
                    <TableRow key={`${row.kind}-${row.owner}-${row.model}`} className='transition-colors'>
                      <TableCell className='py-2.5'>
                        <Badge
                          variant='secondary'
                          className={cn(
                            'text-[10px] font-semibold gap-1 py-0.5',
                            isUser
                              ? 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
                              : 'border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300'
                          )}
                        >
                          {isUser ? <User className='size-2.5' /> : <Users className='size-2.5' />}
                          {isUser ? t('User') : t('Customer group', { defaultValue: '客户组' })}
                        </Badge>
                      </TableCell>
                      <TableCell className='py-2.5 font-medium text-xs text-stone-900 dark:text-stone-100'>
                        {row.owner}
                      </TableCell>
                      <TableCell className='py-2.5'>
                        <code className='rounded bg-stone-100 px-1.5 py-0.5 font-mono text-xs font-semibold text-stone-800 dark:bg-stone-800 dark:text-stone-200 break-all'>
                          {row.model}
                        </code>
                      </TableCell>
                      <TableCell className='py-2.5 text-right font-mono text-xs'>
                        <span className='font-bold text-emerald-600 dark:text-emerald-400'>
                          {Number((row.factor * 100).toFixed(4))}% · {discountText(row.factor)}
                        </span>
                        <span className='text-stone-400 ml-1.5 text-[11px]'>
                          (×{row.factor})
                        </span>
                      </TableCell>
                      <TableCell className='py-2.5 text-right'>
                        <Button
                          type='button'
                          variant='ghost'
                          size='sm'
                          onClick={() => setDeleting(row)}
                          className='h-7 px-2 text-xs text-stone-400 hover:text-destructive hover:bg-destructive/10'
                        >
                          <Trash2 className='size-3 mr-1' />
                          {t('Delete')}
                        </Button>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      {error && (
        <Alert variant='destructive' className='py-3'>
          <AlertCircle className='size-4' />
          <AlertDescription className='space-y-2'>
            <p className='text-xs'>{error}</p>
            <Button
              type='button'
              variant='outline'
              size='sm'
              className='h-7 text-xs'
              onClick={() => {
                if (
                  window.confirm(
                    '重新加载将放弃未保存的草稿，读取服务器最新规则。继续？'
                  )
                ) {
                  void props.onReload()
                }
              }}
            >
              重新加载最新规则
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {/* 底部保存动作栏 */}
      <div className='flex flex-wrap items-center justify-between gap-3 border-t border-stone-200/80 pt-4 dark:border-stone-800'>
        <div className='flex items-center gap-2'>
          {dirty ? (
            <div className='inline-flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400'>
              <span className='size-2 rounded-full bg-amber-500 animate-pulse' />
              <span role='status' className='font-semibold'>{t('Unsaved changes')}</span>
              <span className='text-stone-400 font-normal'>（有未保存的修改，请点击保存生效）</span>
            </div>
          ) : (
            <span className='inline-flex items-center gap-1.5 text-xs text-stone-400'>
              <Check className='size-3 text-emerald-500' />
              所有规则已同步至云端
            </span>
          )}
        </div>

        <div className='flex items-center gap-2'>
          {dirty && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={save.isPending}
              onClick={() => setRules(JSON.parse(saved))}
              className='h-8 text-xs'
            >
              放弃更改
            </Button>
          )}
          <Button
            type='button'
            onClick={() => {
              setError('')
              save.mutate(rules)
            }}
            disabled={!dirty || save.isPending}
            className='h-8 px-4 text-xs font-semibold'
          >
            {save.isPending ? t('Saving...') : t('Save all rules', { defaultValue: '保存全部规则' })}
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
        title={t('Delete rule', { defaultValue: '删除此规则？' })}
        desc={
          deleting
            ? `${deleting.kind === 'users' ? t('User') : t('Customer group', { defaultValue: '客户组' })} ${deleting.owner} · ${deleting.model} · ${discountText(deleting.factor)}`
            : ''
        }
        confirmText={t('Delete')}
        destructive
        handleConfirm={() => {
          if (deleting) setRules(removeDiscountRule(rules, deleting))
          setDeleting(null)
        }}
      />
    </div>
  )
}
