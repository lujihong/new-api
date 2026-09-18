import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, useMemo, useEffect, useRef } from 'react'
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
  parseDiscountPercent,
  replaceDiscountRule,
  removeDiscountRule,
  type DiscountRow,
  type ModelDiscountRules,
} from './model-discount-rules'

export function ModelDiscountRulesCard() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  return <SessionDiscountCard key={JSON.stringify([userId, sid])} userId={userId} sid={sid} />
}

function SessionDiscountCard({ userId, sid }: { userId?: number; sid?: string }) {
  const { t } = useTranslation()
  const [reload, setReload] = useState(0)
  const [refreshing, setRefreshing] = useState(false)

  const query = useQuery({
    queryKey: ['model-discount-rules', userId, sid],
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
                'Model discount group price explanation',
                {
                  defaultValue:
                    '客户组来自用户管理中的分组，用户指平台上的具体账号。个人规则优先于客户组规则，两者不叠乘；所选折扣再乘以现有有效组倍率。100% 表示不额外优惠，不会取消原有组价。仅匹配客户请求中的精确模型名。',
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
            {query.data ? <p>后台读取失败，当前草稿和未加入的输入已保留。可使用下方刷新按钮重试。</p> : (
              <Button variant='outline' size='sm' disabled={query.isFetching} onClick={() => void query.refetch()}>
                {t('Retry')}
              </Button>
            )}
          </div>
        )}
        {query.data && (
          <DiscountEditor
            key={reload}
            initialRules={query.data}
            refreshing={refreshing}
            fetching={query.isFetching}
            onReload={async () => {
              setRefreshing(true)
              try {
                const result = await query.refetch()
                if (result.isSuccess) setReload((value) => value + 1)
              } finally {
                setRefreshing(false)
              }
            }}
          />
        )}
      </CardContent>
    </Card>
  )
}

function DiscountEditor(props: {
  initialRules: ModelDiscountRules
  refreshing: boolean
  fetching: boolean
  onReload: () => Promise<void>
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const mounted = useRef(true)
  const operation = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])
  function isCurrentSession() {
    const auth = useAuthStore.getState().auth
    return mounted.current && auth.user?.id === userId && auth.session?.sid === sid
  }

  const [rules, setRules] = useState(props.initialRules)
  const [saved, setSaved] = useState(() => JSON.stringify(props.initialRules))
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
  const [editing, setEditing] = useState<DiscountRow | null>(null)
  const [freeRule, setFreeRule] = useState<{ row: DiscountRow; replace: boolean } | null>(null)
  const [tablePage, setTablePage] = useState(1)

  const serializedRules = useMemo(() => JSON.stringify(rules), [rules])
  const dirty = serializedRules !== saved
  const formDirty = editing !== null || kind !== 'groups' || owner !== '' || model !== '' || percent !== '100' || keyword !== ''
  function clearForm() {
    setEditing(null)
    setKind('groups')
    setOwner('')
    setModel('')
    setPercent('100')
    setKeyword('')
    setSearch('')
    setPage(1)
    setFreeRule(null)
  }

  const groups = useQuery({
    queryKey: ['model-discount-groups', userId, sid],
    queryFn: async () => {
      const response = await getGroups()
      if (!response.success || !Array.isArray(response.data)) {
        throw new Error(response.message || 'Failed to load groups')
      }
      return response.data
    },
  })

  const models = useQuery({
    queryKey: ['model-discount-models', userId, sid],
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
    queryKey: ['model-discount-users', userId, sid, search, page],
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
    mutationFn: async ({ value, baseline }: { value: string; baseline: string }) => {
      const response = await updateSystemOption({
        key: 'ModelDiscountRules',
        value,
        expected_value: baseline,
      })
      if (!response.success) {
        throw new Error(response.message || t('Failed to update setting'))
      }
      return value
    },
    onSuccess: (value) => {
      if (!isCurrentSession()) return
      setSaved(value)
      queryClient.setQueryData(['model-discount-rules', userId, sid], JSON.parse(value))
      void queryClient.invalidateQueries({ queryKey: ['pricing'] })
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
      toast.success(t('Setting updated successfully'))
    },
    onError: (failure: Error) => { if (isCurrentSession()) setError(failure.message) },
    onSettled: () => { operation.current = false },
  })
  const busy = save.isPending || props.refreshing

  async function reloadRules() {
    if (operation.current || busy || props.fetching || freeRule || deleting) return
    if ((dirty || formDirty) && !window.confirm('重新加载将放弃未保存的草稿和表单输入，读取服务器最新规则。继续？')) return
    operation.current = true
    try { await props.onReload() } finally { operation.current = false }
  }

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

  const factor = parseDiscountPercent(percent)
  const validPercent = factor !== null

  const discountText = (value: number) =>
    value === 1
      ? t('No extra model discount', { defaultValue: '沿用组价' })
      : t('Discount tenths', {
          defaultValue: '{{value}}折',
          value: Number((value * 10).toPrecision(15)),
        })

  const discountBadgeVariant = useMemo((): 'outline' | 'secondary' | 'default' => {
    if (factor === 1) return 'outline'
    if (factor === 0) return 'secondary'
    return 'default'
  }, [factor])

  function commitRule(row: DiscountRow, replace: boolean) {
    if (busy || operation.current || !isCurrentSession()) return
    try {
      setRules(replace ? replaceDiscountRule(rules, row) : addDiscountRule(rules, row))
      clearForm()
    } catch {
      setError('该客户与模型已有规则或原行已不存在，请检查规则清单。')
    }
  }

  function addRule() {
    if (busy || operation.current || freeRule) return
    setError('')
    if (factor === null) return
    if (!editing && (!ownerOptions.some((option) => option.value === owner) || !models.data?.includes(model))) return
    const row = editing ? { ...editing, factor } : { kind, owner, model, factor }
    if (factor === 0) {
      setFreeRule({ row, replace: editing !== null })
      return
    }
    commitRule(row, editing !== null)
  }

  function editRule(row: DiscountRow) {
    if (busy || operation.current || formDirty || freeRule) return
    setError('')
    setEditing(row)
    setKind(row.kind)
    setOwner(row.owner)
    setModel(row.model)
    // Preserve historical precision. Invalid legacy precision must be explicitly changed or cancelled.
    setPercent(String(Number((row.factor * 100).toPrecision(15))))
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

  const pageCount = Math.max(1, Math.ceil(filteredRows.length / 25))
  const visiblePage = Math.min(tablePage, pageCount)
  const visibleRows = filteredRows.slice((visiblePage - 1) * 25, visiblePage * 25)

  const PRESET_DISCOUNTS = [
    { label: '9.5折', value: '95' },
    { label: '9折', value: '90' },
    { label: '8.5折', value: '85' },
    { label: '8折', value: '80' },
    { label: '7折', value: '70' },
    { label: '5折', value: '50' },
    { label: '免费 (0折)', value: '0' },
    { label: '沿用组价 (100%)', value: '100' },
  ]

  return (
    <div className='space-y-6'>
      <FormNavigationGuard when={dirty || formDirty} />

      {/* 规则配置操作面板 */}
      <fieldset
        disabled={busy}
        className='rounded-xl border border-stone-200/80 bg-stone-50/50 p-4.5 dark:border-stone-800 dark:bg-stone-900/40 space-y-4'
      >
        <div className='flex items-center justify-between border-b border-stone-200/70 pb-3 dark:border-stone-800'>
          <div className='flex items-center gap-2 text-xs font-bold text-stone-800 dark:text-stone-200'>
            <Plus className='size-4 text-emerald-600 dark:text-emerald-400' />
            <span>{editing ? '编辑规则比例（客户与模型已锁定）' : '新增专属折扣配置'}</span>
          </div>
          <div className='flex items-center gap-2'>
            <span className='text-[11px] text-stone-400'>
              当前草稿 {allRows.length} 条 · 保存后生效
            </span>
          </div>
        </div>

        <div className='grid gap-4 md:grid-cols-3'>
          {/* 规则类型 */}
          <div className='min-w-0 space-y-1.5'>
            <Label className='text-xs font-medium text-stone-700 dark:text-stone-300'>
              {t('Rule type', { defaultValue: '规则类型' })}
            </Label>
            <Combobox
              openOnFocus={false}
              aria-label={t('Rule type', { defaultValue: '规则类型' })}
              value={kind}
              disabled={busy || editing !== null}
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
          <div className='min-w-0 space-y-1.5'>
            <div className='flex items-center justify-between'>
              <Label className='text-xs font-medium text-stone-700 dark:text-stone-300'>
                {t('Discount owner', { defaultValue: '折扣客户' })}
              </Label>
              {kind === 'users' && users.data && (
                <span className='text-[10px] text-stone-400'>
                  下拉仅当前页（每页 20 人）：第 {page} / {Math.max(1, Math.ceil(users.data.total / 20))} 页 · 共 {users.data.total} 人
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
                    disabled={editing !== null}
                    onChange={(event) => setKeyword(event.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !e.nativeEvent.isComposing && e.keyCode !== 229) {
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
                    disabled={editing !== null}
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
                  disabled={busy || editing !== null || catalogLoading || catalogFailed}
                />
                {users.data && users.data.total > 20 && (
                  <div className='flex items-center justify-end gap-1.5 pt-0.5 text-xs'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      className='h-6 px-2 text-[11px]'
                      disabled={busy || editing !== null || page <= 1}
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
                      disabled={busy || editing !== null || page * 20 >= users.data.total}
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
                disabled={busy || editing !== null || catalogLoading || catalogFailed}
              />
            )}
          </div>

          {/* 精确模型选择 */}
          <div className='min-w-0 space-y-1.5'>
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
              disabled={busy || editing !== null || catalogLoading || catalogFailed}
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
                type='text'
                inputMode='decimal'
                aria-describedby='discount-percent-help'
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
                disabled={busy || !!freeRule || !owner || !model || !validPercent || (!editing && (catalogLoading || catalogFailed))}
                className='h-9 gap-1.5 text-xs font-semibold'
              >
                <Plus className='size-3.5' />
                {editing ? '更新草稿规则' : t('Add rule', { defaultValue: '新增规则' })}
              </Button>
              {formDirty && <Button type='button' variant='ghost' disabled={busy} onClick={clearForm}>{editing ? '取消编辑' : '清空输入'}</Button>}
            </div>
          </div>
          <p id='discount-percent-help' role={validPercent ? undefined : 'alert'} className={cn('text-sm', validPercent ? 'text-muted-foreground' : 'text-destructive')}>
            {validPercent ? '支持 0–100，最多六位小数。0 表示免费，100 表示沿用现有组价。' : '请输入 0–100 的普通十进制数，最多六位小数，不接受科学计数法。'}
          </p>
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

          <div className='w-full sm:w-56'>
            <Input
              aria-label='筛选规则或模型'
              placeholder='筛选规则或模型...'
              value={tableFilter}
              onChange={(e) => { setTableFilter(e.target.value); setTablePage(1) }}
              className='h-8 text-xs'
            />
          </div>
        </div>

        {allRows.length === 0 ? (
          <div className='flex flex-col items-center justify-center rounded-xl border border-dashed border-stone-200/90 bg-stone-50/40 py-10 text-center dark:border-stone-800 dark:bg-stone-900/20'>
            <div className='flex size-10 items-center justify-center rounded-full bg-stone-100 text-stone-400 dark:bg-stone-800 dark:text-stone-500 mb-2.5'>
              <Sparkles className='size-5' />
            </div>
            <p className='text-sm font-medium text-stone-600 dark:text-stone-400'>
              {t('No additional model discounts', {
                defaultValue: '暂无折扣规则，沿用现有组价，不额外优惠（×1）。',
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
                {filteredRows.length === 0 && <TableRow><TableCell colSpan={5} className='py-8 text-center text-muted-foreground'>没有匹配的规则，请修改筛选条件。</TableCell></TableRow>}
                {visibleRows.map((row) => {
                  const isUser = row.kind === 'users'
                  return (
                    <TableRow key={JSON.stringify([row.kind, row.owner, row.model])} className='transition-colors'>
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
                      <TableCell className='min-w-0 py-2.5 font-medium text-sm text-stone-900 dark:text-stone-100 break-words'>
                        {row.owner}
                      </TableCell>
                      <TableCell className='min-w-0 py-2.5'>
                        <code className='rounded bg-stone-100 px-1.5 py-0.5 font-mono text-xs font-semibold text-stone-800 dark:bg-stone-800 dark:text-stone-200 break-all'>
                          {row.model}
                        </code>
                      </TableCell>
                      <TableCell className='py-2.5 text-right font-mono text-xs'>
                        <span className='font-bold text-emerald-600 dark:text-emerald-400'>
                          {Number((row.factor * 100).toPrecision(15))}% · {discountText(row.factor)}
                        </span>
                        <span className='text-stone-400 ml-1.5 text-[11px]'>
                          (×{row.factor})
                        </span>
                      </TableCell>
                      <TableCell className='py-2.5 text-right'>
                        <Button type='button' variant='ghost' size='sm' disabled={busy || formDirty || !!freeRule} onClick={() => editRule(row)}>编辑</Button>
                        <Button
                          type='button'
                          variant='ghost'
                          size='sm'
                          disabled={busy || formDirty || !!freeRule}
                          onClick={() => { if (!busy && !operation.current) setDeleting(row) }}
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
        {filteredRows.length > 25 && <div className='flex flex-wrap items-center justify-between gap-2 text-sm'>
          <span>共 {filteredRows.length} 条 · 第 {visiblePage} / {pageCount} 页（每页 25 条）</span>
          <div className='flex gap-2'>
            <Button type='button' variant='outline' size='sm' aria-label='规则上一页' disabled={visiblePage <= 1} onClick={() => setTablePage(visiblePage - 1)}>上一页</Button>
            <Button type='button' variant='outline' size='sm' aria-label='规则下一页' disabled={visiblePage >= pageCount} onClick={() => setTablePage(visiblePage + 1)}>下一页</Button>
          </div>
        </div>}
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
              disabled={busy || props.fetching || !!freeRule || !!deleting}
              onClick={() => void reloadRules()}
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
              当前规则无未保存修改
            </span>
          )}
        </div>

        <div className='flex items-center gap-2'>
          {dirty && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={busy}
              onClick={() => {
                if (operation.current || !window.confirm('放弃尚未保存的规则修改和表单输入？')) return
                setRules(JSON.parse(saved)); clearForm(); setError('')
              }}
              className='h-8 text-xs'
            >
              放弃更改
            </Button>
          )}
          <Button type='button' variant='outline' disabled={busy || props.fetching || !!freeRule || !!deleting} onClick={() => void reloadRules()}>刷新规则</Button>
          <Button
            type='button'
            onClick={() => {
              if (operation.current || busy || formDirty || freeRule || deleting || !isCurrentSession()) return
              operation.current = true
              setError('')
              save.mutate({ value: serializedRules, baseline: saved })
            }}
            disabled={!dirty || busy || formDirty || !!freeRule || !!deleting}
            className='h-8 px-4 text-xs font-semibold'
          >
            {save.isPending ? t('Saving...') : t('Save all rules', { defaultValue: '保存全部规则' })}
          </Button>
        </div>
      </div>

      {formDirty && <p role='status' className='text-sm text-muted-foreground'>表单尚未加入规则清单，请先新增、更新或取消，再保存全部规则。</p>}
      <ConfirmDialog
        open={freeRule !== null}
        onOpenChange={(open) => { if (!open) setFreeRule(null) }}
        title='确认设置免费规则？'
        desc={freeRule ? `${freeRule.row.kind === 'users' ? '用户' : '客户组'} ${freeRule.row.owner} · ${freeRule.row.model} 将设为 0%，保存全部规则后生效。上游仍可能产生采购费用。` : ''}
        confirmText='确认免费'
        disabled={busy}
        handleConfirm={() => { if (freeRule && !busy && !operation.current) { commitRule(freeRule.row, freeRule.replace); setFreeRule(null) } }}
      />
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
        disabled={busy}
        handleConfirm={() => {
          if (busy || operation.current || !isCurrentSession()) return
          if (deleting) {
            setRules(removeDiscountRule(rules, deleting))
            setTablePage(Math.min(visiblePage, Math.max(1, Math.ceil((filteredRows.length - 1) / 25))))
          }
          setDeleting(null)
        }}
      />
    </div>
  )
}
