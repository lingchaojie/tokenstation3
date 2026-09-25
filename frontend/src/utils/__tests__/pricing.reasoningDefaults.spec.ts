import { describe, expect, it } from 'vitest'
import { defaultReasoningEffortMultiplier } from '../pricing'

describe('preserved reasoning effort defaults', () => {
  it.each(['claude-fable-5-1', ' FABLE-5.1 ', 'fable5.1-20260925', 'vendor/fable51:latest'])(
    'uses 3x only for max on %s', model => {
      expect(defaultReasoningEffortMultiplier(model, 'max')).toBe(3)
      expect(defaultReasoningEffortMultiplier(model, 'high')).toBe(1)
    },
  )
  it.each(['fable-5-11', 'fable5.11', 'fable510', 'claude-opus-5-5'])('does not uplift %s', model => {
    expect(defaultReasoningEffortMultiplier(model, 'max')).toBe(1)
  })
})
