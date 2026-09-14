import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { setTimeout as sleepDefault } from 'node:timers/promises'
import { pathToFileURL } from 'node:url'

const terminal = (run) => ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(run.status)
const prompt = 'Use Python to inspect the uploaded CSV and calculate value totals and coordinate summaries. Produce an offline ECharts HTML report plus analysis-evidence.json and the artifact manifest required by the Geo Analyst profile. Do not access the network.'

// External I/O is injectable so the default test suite never contacts Claude.
export async function runSmoke({
  fetch = globalThis.fetch, chromium, csv,
  baseURL = process.env.SMOKE_API_URL ?? 'http://localhost:8080',
  sleep = sleepDefault, now = Date.now, log = console.error,
} = {}) {
  const request = async (path, init = {}, deadline = now() + 15000) => {
    const response = await fetch(new URL(path, baseURL), {
      ...init, signal: AbortSignal.timeout(Math.max(1, Math.min(15000, deadline - now()))),
    })
    if (!response.ok) throw Object.assign(new Error(`${init.method ?? 'GET'} ${path}: HTTP ${response.status}`), { status: response.status })
    return response.status === 204 ? null : response.json()
  }
  const post = (path, body) => request(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
  let project, run, browser, failure
  const cleanupErrors = []
  const cleanup = async (action) => {
    try { await action() } catch (error) { cleanupErrors.push(error); log(`Cleanup: ${error.message}`) }
  }
  try {
    csv ??= await readFile(new URL('../../../tests/fixtures/geo/locations.csv', import.meta.url))
    const digest = createHash('sha256').update(csv).digest('hex')
    const rowCount = csv.toString('utf8').trim().split(/\r?\n/).length - 1
    project = await post('/api/v1/projects', { name: 'Geo Claude smoke', profile_id: 'geo-analysis' })
    const form = new FormData()
    form.set('file', new Blob([csv], { type: 'text/csv' }), 'locations.csv')
    const uploaded = await request(`/api/v1/projects/${project.id}/inputs`, { method: 'POST', body: form })
    assert.equal(uploaded.sha256_digest, digest, 'uploaded input digest mismatch')
    const conversation = await post(`/api/v1/projects/${project.id}/conversations`, { title: 'Geo smoke' })
    ;({ run } = await post(`/api/v1/conversations/${conversation.id}/messages`, { content: prompt }))
    const deadline = now() + 120000
    while (true) {
      if (now() >= deadline) throw new Error('Run timed out after 120 seconds')
      run = await request(`/api/v1/runs/${run.id}`, {}, deadline)
      if (terminal(run)) break
      await sleep(Math.min(1000, Math.max(0, deadline - now())))
    }
    assert.equal(run.status, 'succeeded', `Run ${run.status}`)
    const events = await request(`/api/v1/runs/${run.id}/events`)
    assert(events.some((event) => /^(assistant\.|tool\.)/.test(event.type)), 'missing assistant/tool connectivity event')
    const pythonCalls = new Set(events.filter((event) => event.type === 'tool.started'
      && event.payload.name === 'Bash'
      && /(?:^|[;&|\n])\s*(?:[\w./-]*\/)?python(?:3(?:\.\d+)?)?(?=\s|$)/.test(event.payload.input?.command ?? ''))
      .map((event) => event.payload.tool_call_id))
    assert(events.some((event) => event.type === 'tool.completed' && event.payload.name === 'Bash'
      && event.payload.outcome === 'succeeded' && pythonCalls.has(event.payload.tool_call_id)), 'missing successful Python tool execution')
    const artifacts = await request(`/api/v1/runs/${run.id}/artifacts`)
    const primary = artifacts.filter((artifact) => artifact.is_primary)
    assert.equal(primary.length, 1, 'expected exactly one primary artifact')
    assert.equal(primary[0].type, 'html', 'primary must be HTML')
    const evidenceArtifact = artifacts.find((artifact) => artifact.type === 'data' && artifact.entry_path === 'data/analysis-evidence.json')
    assert(evidenceArtifact, 'missing analysis-evidence.json data artifact')
    const evidence = await request(evidenceArtifact.gateway_url)
    assert(evidence.inputs?.some((input) => input.input_digest === digest && input.row_count === rowCount
      && input.computed_fields && typeof input.computed_fields === 'object'
      && !Array.isArray(input.computed_fields) && Object.keys(input.computed_fields).length > 0), 'invalid analysis evidence')
    chromium ??= (await import('@playwright/test')).chromium
    browser = await chromium.launch({ timeout: 15000 })
    const page = await browser.newPage()
    const browserErrors = []
    page.on('pageerror', (error) => browserErrors.push(error.message))
    page.on('requestfailed', (req) => browserErrors.push(`${req.url()}: ${req.failure()?.errorText}`))
    page.on('response', (response) => { if (!response.ok()) browserErrors.push(`HTTP ${response.status()}: ${response.url()}`) })
    const response = await page.goto(primary[0].gateway_url, { waitUntil: 'networkidle', timeout: 15000 })
    assert(response?.ok(), 'primary report failed to load')
    await page.waitForFunction(() => document.body?.textContent?.trim() && window.echarts, { }, { timeout: 15000 })
    assert.equal(browserErrors.length, 0, browserErrors.join('\n'))
    log(`Smoke passed: Run ${run.id}`)
  } catch (error) {
    failure = error
    if (run) {
      try { run = await request(`/api/v1/runs/${run.id}`) } catch (diagnostic) { log(`Run diagnostic: ${diagnostic.message}`) }
      log(`Run: ${JSON.stringify(run)}`)
      try { log(`Events: ${JSON.stringify(await request(`/api/v1/runs/${run.id}/events`))}`) }
      catch (diagnostic) { log(`Events: unavailable (${diagnostic.message})`) }
    }
    throw error
  } finally {
    if (browser) await cleanup(() => browser.close())
    if (run && !terminal(run)) {
      await cleanup(async () => {
        try { await post(`/api/v1/runs/${run.id}/cancel`, {}) }
        catch (error) {
          if (error.status !== 409) throw error
          // Completion can win between our snapshot and the cancellation request.
          run = await request(`/api/v1/runs/${run.id}`)
          if (!terminal(run)) throw error
        }
      })
    }
    if (run && (!terminal(run) || !run.finalized_at)) {
      await cleanup(async () => {
        const deadline = now() + 30000
        while (now() < deadline) {
          run = await request(`/api/v1/runs/${run.id}`, {}, deadline)
          if (terminal(run) && run.finalized_at) return
          await sleep(Math.min(1000, Math.max(0, deadline - now())))
        }
        throw new Error('Run cleanup timed out waiting for finalized_at')
      })
    }
    if (project) await cleanup(() => request(`/api/v1/projects/${project.id}`, { method: 'DELETE' }))
    if (!failure && cleanupErrors.length) throw cleanupErrors[0]
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  if (!process.env.ANTHROPIC_API_KEY?.trim()) {
    console.error('ANTHROPIC_API_KEY is required')
    process.exitCode = 1
  } else {
    await runSmoke().catch((error) => { console.error(error.message); process.exitCode = 1 })
  }
}
