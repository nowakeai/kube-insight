export type ComposerContextBlock = {
  id: string
  title: string
  text: string
}

export function formatPromptWithContextBlocks(prompt: string, blocks: ComposerContextBlock[]) {
  const trimmed = prompt.trim()
  if (blocks.length === 0) return trimmed
  const header = trimmed || "Use the pasted context below."
  const context = blocks.map((block, index) => [
    `<context_block index="${index + 1}" title="${escapeContextTitle(block.title)}">`,
    block.text.trim(),
    "</context_block>",
  ].join("\n")).join("\n\n")
  return `${header}\n\n${context}`
}

function escapeContextTitle(value: string) {
  return value.replaceAll("&", "&amp;").replaceAll("\"", "&quot;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
}
