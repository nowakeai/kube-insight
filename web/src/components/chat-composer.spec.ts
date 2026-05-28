import { expect, test } from "@playwright/test"

import { formatPromptWithContextBlocks, type ComposerContextBlock } from "@/lib/composer-context"

test("formatPromptWithContextBlocks appends pasted context blocks", () => {
  const blocks: ComposerContextBlock[] = [
    { id: "ctx_1", title: "Pod describe", text: "Events:\nOOMKilled" },
  ]

  expect(formatPromptWithContextBlocks("why did it restart?", blocks)).toBe([
    "why did it restart?",
    "",
    "<context_block index=\"1\" title=\"Pod describe\">",
    "Events:\nOOMKilled",
    "</context_block>",
  ].join("\n"))
})

test("formatPromptWithContextBlocks provides a prompt when only context is pasted", () => {
  const blocks: ComposerContextBlock[] = [
    { id: "ctx_1", title: "Logs", text: "line 1" },
  ]

  expect(formatPromptWithContextBlocks("", blocks)).toContain("Use the pasted context below.")
})
