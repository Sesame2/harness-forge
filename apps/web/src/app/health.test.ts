import { afterEach, describe, expect, it, vi } from 'vitest'

import { loadHealth } from './health'

describe('loadHealth', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('requests health from the same origin', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ status: 'ok' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetch)

    await expect(loadHealth()).resolves.toEqual({ status: 'ok' })
    expect(fetch).toHaveBeenCalledWith('/health')
  })

  it('throws an error containing the response status for non-2xx responses', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 503 })))

    await expect(loadHealth()).rejects.toThrow('503')
  })
})
