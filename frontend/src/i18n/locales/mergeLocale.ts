export function mergeLocaleMissing<T extends Record<string, unknown>>(
  base: T,
  extra: Record<string, unknown>
): T {
  const out: Record<string, unknown> = { ...base }
  for (const [key, val] of Object.entries(extra)) {
    const cur = out[key]
    if (cur === undefined) {
      out[key] = val
    } else if (
      cur && typeof cur === 'object' && !Array.isArray(cur) &&
      val && typeof val === 'object' && !Array.isArray(val)
    ) {
      out[key] = mergeLocaleMissing(cur as Record<string, unknown>, val as Record<string, unknown>)
    }
  }
  return out as T
}
