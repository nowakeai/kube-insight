import { useEffect, useRef, useState, type RefObject } from "react"

export function useElementVisibility<T extends Element>(options: IntersectionObserverInit = {}): [RefObject<T | null>, boolean] {
  const ref = useRef<T | null>(null)
  const [visible, setVisible] = useState(true)
  const { root, rootMargin, threshold } = options

  useEffect(() => {
    const element = ref.current
    if (!element || typeof IntersectionObserver === "undefined") {
      setVisible(true)
      return undefined
    }
    const observer = new IntersectionObserver((entries) => {
      const entry = entries[0]
      setVisible(Boolean(entry?.isIntersecting))
    }, { root, rootMargin, threshold })
    observer.observe(element)
    return () => observer.disconnect()
  }, [root, rootMargin, threshold])

  return [ref, visible]
}
