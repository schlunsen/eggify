export interface ModelChoice { provider: string; model: string; providerName: string; icon: string }
export interface ModelProvider { id: string; name: string; icon: string; models: string[] }
export const MODEL_PINS_KEY = 'eggify:model-pins:v1'
// How many favourites can be pinned, and how many quick picks the picker shows.
export const MAX_MODEL_PINS = 5
export const modelKey = (choice: { provider: string; model: string }) => JSON.stringify([choice.provider, choice.model])

export function loadModelPins(): string[] {
  try {
    const data = JSON.parse(localStorage.getItem(MODEL_PINS_KEY) || '[]')
    return Array.isArray(data) ? [...new Set(data.filter((key): key is string => typeof key === 'string'))].slice(0, MAX_MODEL_PINS) : []
  } catch { return [] }
}

export function saveModelPins(pins: string[]) {
  try { localStorage.setItem(MODEL_PINS_KEY, JSON.stringify(pins.slice(0, MAX_MODEL_PINS))) } catch { /* Storage may be unavailable. */ }
}

export function rankModelChoices(providers: ModelProvider[], sessions: { provider?: string; model_name?: string; options?: { provider?: string | null; model?: string | null } }[], pins: string[], current: { provider: string; model: string }): ModelChoice[] {
  const counts = new Map<string, number>()
  for (const session of sessions) {
    const provider = session.options?.provider || session.provider
    const model = session.options?.model || session.model_name
    if (!provider || !model) continue
    const key = modelKey({ provider, model })
    counts.set(key, (counts.get(key) || 0) + 1)
  }
  const choices = providers.flatMap(p => p.models.map(model => ({ provider: p.id, model, providerName: p.name, icon: p.icon })))
  return choices.sort((a, b) => {
    const ak = modelKey(a), bk = modelKey(b)
    const ai = pins.indexOf(ak), bi = pins.indexOf(bk)
    if (ai >= 0 || bi >= 0) return (ai < 0 ? 3 : ai) - (bi < 0 ? 3 : bi)
    return (counts.get(bk) || 0) - (counts.get(ak) || 0)
      || Number(bk === modelKey(current)) - Number(ak === modelKey(current))
  })
}
