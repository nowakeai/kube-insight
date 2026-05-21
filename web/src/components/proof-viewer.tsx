import { json } from "@codemirror/lang-json"
import { yaml } from "@codemirror/lang-yaml"
import CodeMirror from "@uiw/react-codemirror"

export function ProofViewer({
  value,
  language = "json",
}: {
  value: unknown
  language?: "json" | "yaml"
}) {
  const text = proofText(value, language)
  return (
    <div className="overflow-hidden rounded-md border border-border bg-muted text-xs">
      <CodeMirror
        value={text}
        height="18rem"
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: false,
          highlightActiveLineGutter: false,
        }}
        extensions={[language === "yaml" ? yaml() : json()]}
        editable={false}
        theme="light"
      />
    </div>
  )
}

function proofText(value: unknown, language: "json" | "yaml") {
  if (typeof value === "string") return value
  if (language === "yaml") return objectToYaml(value)
  return JSON.stringify(value, null, 2)
}

function objectToYaml(value: unknown, indent = 0): string {
  if (value === null || value === undefined) return "null"
  if (typeof value !== "object") return scalarToYaml(value)
  const pad = " ".repeat(indent)
  if (Array.isArray(value)) {
    if (value.length === 0) return "[]"
    return value.map((item) => `${pad}- ${nestedYaml(item, indent + 2)}`).join("\n")
  }
  const entries = Object.entries(value as Record<string, unknown>)
  if (entries.length === 0) return "{}"
  return entries
    .map(([key, item]) => `${pad}${key}: ${nestedYaml(item, indent + 2)}`)
    .join("\n")
}

function nestedYaml(value: unknown, indent: number) {
  if (value && typeof value === "object") return `\n${objectToYaml(value, indent)}`
  return scalarToYaml(value)
}

function scalarToYaml(value: unknown) {
  if (typeof value === "string") return JSON.stringify(value)
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return "null"
}
