import { expect, test } from "@playwright/test"

import { randomStarterPrompts } from "@/lib/starter-prompts"

test("randomStarterPrompts returns a bounded unique subset", () => {
  const prompts = randomStarterPrompts(4, () => 0.42)

  expect(prompts).toHaveLength(4)
  expect(new Set(prompts).size).toBe(4)
})

test("randomStarterPrompts handles zero count", () => {
  expect(randomStarterPrompts(0, () => 0.42)).toEqual([])
})
