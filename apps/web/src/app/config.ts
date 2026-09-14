// Fail closed: backend resources supply paths; config supplies only the trusted origin.
export function artifactURL(value: string, configured = import.meta.env.VITE_ARTIFACT_ORIGIN, webOrigin = window.location.origin): URL | null {
  try {
    const origin = new URL(configured)
    const url = new URL(value)
    if (!['http:', 'https:'].includes(origin.protocol) || origin.username || origin.password
      || origin.pathname !== '/' || origin.search || origin.hash || origin.origin === webOrigin
      || url.protocol !== origin.protocol || url.origin !== origin.origin || url.username || url.password) return null
    return url
  } catch { return null }
}
