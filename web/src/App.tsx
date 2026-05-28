import { useEffect, useState } from "react"

import { AgentChat } from "@/components/agent-chat"
import { Dashboard } from "@/components/dashboard"

function App() {
  const pathname = usePathname()
  if (pathname === "/dashboard") return <Dashboard />
  return <AgentChat />
}

function usePathname() {
  const [pathname, setPathname] = useState(() => window.location.pathname)

  useEffect(() => {
    const onPopState = () => setPathname(window.location.pathname)
    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [])

  return pathname
}

export default App
