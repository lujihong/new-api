import { z } from 'zod'

const factors = z.record(z.string().min(1), z.number().finite().min(0).max(1))
const owners = z.record(z.string().min(1), factors)
export const modelDiscountRulesSchema = z.object({
  groups: owners.optional(),
  users: owners.optional(),
}).strict()
export type ModelDiscountRules = z.infer<typeof modelDiscountRulesSchema>
export type DiscountRow = { kind: 'groups' | 'users'; owner: string; model: string; factor: number }

export function parseDiscountRules(value: string): ModelDiscountRules {
  return modelDiscountRulesSchema.parse(JSON.parse(value))
}

export function discountRows(rules: ModelDiscountRules): DiscountRow[] {
  return (['groups', 'users'] as const).flatMap((kind) =>
    Object.entries(rules[kind] ?? {}).flatMap(([owner, models]) =>
      Object.entries(models).map(([model, factor]) => ({ kind, owner, model, factor }))
    )
  )
}

export function addDiscountRule(rules: ModelDiscountRules, row: DiscountRow): ModelDiscountRules {
  if (Object.hasOwn(rules[row.kind]?.[row.owner] ?? {}, row.model)) {
    throw new Error('A rule already exists for this owner and model')
  }
  return modelDiscountRulesSchema.parse({
    ...rules,
    [row.kind]: {
      ...rules[row.kind],
      [row.owner]: { ...rules[row.kind]?.[row.owner], [row.model]: row.factor },
    },
  })
}

export function removeDiscountRule(rules: ModelDiscountRules, row: DiscountRow): ModelDiscountRules {
  const next = structuredClone(rules)
  const owner = next[row.kind]?.[row.owner]
  if (owner) {
    delete owner[row.model]
    if (!Object.keys(owner).length) delete next[row.kind]?.[row.owner]
    if (!Object.keys(next[row.kind] ?? {}).length) delete next[row.kind]
  }
  return next
}
