import { createFileRoute } from '@tanstack/react-router'
import { AiccAssets } from '@/features/aicc'

export const Route = createFileRoute('/_authenticated/aicc-assets/')({
  component: AiccAssets,
})
