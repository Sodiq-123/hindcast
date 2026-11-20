import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { ApiError, auth, type Me } from './api'

interface AuthState {
  /** undefined while loading, null when signed out */
  me: Me | null | undefined
  setMe: (m: Me | null) => void
  refresh: () => Promise<void>
  signOut: () => Promise<void>
}

const Ctx = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null | undefined>(undefined)
  const refresh = useCallback(async () => {
    try {
      setMe(await auth.me())
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setMe(null)
      else throw e
    }
  }, [])
  useEffect(() => {
    refresh().catch(() => setMe(null))
    const out = () => setMe(null)
    window.addEventListener('auth:signed-out', out)
    return () => window.removeEventListener('auth:signed-out', out)
  }, [refresh])
  const signOut = useCallback(async () => {
    await auth.logout().catch(() => {})
    setMe(null)
  }, [])
  return <Ctx.Provider value={{ me, setMe, refresh, signOut }}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAuth outside AuthProvider')
  return c
}

export const useIsAdmin = () => useMe().user.role === 'admin'

/** The signed-in person (only use below the signed-in part of the app). */
export function useMe(): Me {
  const { me } = useAuth()
  if (!me) throw new Error('useMe while signed out')
  return me
}
