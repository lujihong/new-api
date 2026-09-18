/*
Copyright (C) 2023-2026 QuantumNous

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
export type VideoSecondPriceProjection = {
  tiers: { label: string; pricePerSecond: number }[]
  fallbackSeconds: 5
  fallbackResolution: '720p'
}

// Display-only grammar, deliberately separate from the token AST. Every leaf
// must be exactly (vs == 0 ? 5 : vs) * a finite non-negative decimal literal.
// Never strip whitespace inside identifiers/strings or evaluate supplied code.
const decimal = String.raw`(?:0|[1-9]\d*)(?:\.\d+)?`
function secondTier(label: string, capture: string): string {
  return String.raw`tier\s*\(\s*"${label}"\s*,\s*\(\s*vs\s*==\s*0\s*\?\s*5\s*:\s*vs\s*\)\s*\*\s*(?<${capture}>${decimal})\s*\)`
}
function resolutionCondition(resolution: string): string {
  return String.raw`has\s*\(\s*param\s*\(\s*"resolution"\s*\)\s*,\s*"${resolution}"\s*\)`
}
const secondPricePattern = new RegExp(
  String.raw`^(?:${resolutionCondition('1080')}\s*\?\s*${secondTier('1080p', 'high')}\s*:\s*)?${resolutionCondition('480')}\s*\?\s*${secondTier('480p', 'low')}\s*:\s*${secondTier('720p', 'middle')}$`
)

/** Recognize only the two supported resolution trees, consuming the whole source. */
export function parseVideoSecondPrice(
  expression: string
): VideoSecondPriceProjection | null {
  const source = expression.trim()
  const match = secondPricePattern.exec(source)
  if (!match?.groups || match[0].length !== source.length) return null
  const tiers = [
    ...(match.groups.high === undefined
      ? []
      : [{ label: '1080p', pricePerSecond: Number(match.groups.high) }]),
    { label: '480p', pricePerSecond: Number(match.groups.low) },
    { label: '720p', pricePerSecond: Number(match.groups.middle) },
  ]
  if (tiers.some((tier) => !Number.isFinite(tier.pricePerSecond))) return null
  return { tiers, fallbackSeconds: 5, fallbackResolution: '720p' }
}
