export interface Health {
  status: string
}

export async function loadHealth(): Promise<Health> {
  const response = await fetch('/health')
  if (!response.ok) {
    throw new Error(`health request failed with status ${response.status}`)
  }
  return response.json() as Promise<Health>
}
