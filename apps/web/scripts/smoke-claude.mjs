import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { setTimeout as sleepDefault } from 'node:timers/promises'
import { pathToFileURL } from 'node:url'

const terminal = (run) => ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(run.status)
const prompt = 'Use Python to inspect the uploaded CSV and calculate value totals and coordinate summaries. Produce an offline ECharts HTML report plus analysis-evidence.json and the artifact manifest required by the Geo Analyst profile. Do not access the network.'
const fullPrompt = `${prompt} Compute the sum of the value column as computed_fields.value_sum. This is a fresh Run workspace: discover this Run's input files and recreate every required output and local asset, even when continuing a conversation. Work within eight model turns and at most four tool calls. First inspect the actual CSV header and rows with one Bash/Python call. Then batch calculation, evidence, HTML, manifest writing and local asset copying into one Bash/Python script, using csv, pathlib, json, hashlib and shutil as needed. Do not inspect unrelated files or print large asset contents. Output tool calls only until all files exist; do not include explanatory text alongside a tool call. Then finish with one brief text summary.`
const uuid = (value) => typeof value === 'string' && /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/.test(value)
const safeError = (error) => Number.isInteger(error?.status) && error.status >= 400 && error.status <= 599 ? `HTTP_${error.status}`
  : error?.message === 'Run cleanup timed out waiting for finalized_at' ? 'finalized_timeout' : 'operation_failed'
const safeRun = (run) => ({
  ...Object.fromEntries(['id', 'conversation_id', 'source_sdk_session_id', 'candidate_sdk_session_id'].map((key) => [key, uuid(run?.[key]) ? run[key] : null])),
  status: ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'interrupted'].includes(run?.status) ? run.status : 'unknown',
  phase: ['preparing', 'agent', 'publishing'].includes(run?.phase) ? run.phase : null,
  finalized: Boolean(run?.finalized_at),
  error: run?.error ? { code: ['worker_failed', 'execution_failed', 'manifest_invalid', 'manifest_mismatch', 'publishing_failed', 'preparation_failed', 'acquire_failed', 'cancelled', 'interrupted', 'claude_stream_failed'].includes(run.error.code) ? run.error.code : 'unknown' } : null,
})
const safeEvents = (events) => events.map((event) => ({ sequence: Number.isSafeInteger(event.sequence) && event.sequence > 0 ? event.sequence : null,
  type: ['phase.changed', 'assistant.delta', 'assistant.message', 'tool.started', 'tool.completed', 'artifact.candidate', 'agent.completed', 'agent.failed', 'run.succeeded', 'run.failed', 'run.cancelled', 'run.interrupted'].includes(event.type) ? event.type : 'unknown' }))

// External I/O is injectable so the default test suite never contacts Claude.
export async function runSmoke({
  fetch = globalThis.fetch, chromium, csv,
  baseURL = process.env.SMOKE_API_URL ?? 'http://localhost:8080',
  fullUI = false, webURL = process.env.SMOKE_WEB_URL ?? 'http://localhost:5173',
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
  let project, run, browser, page, failure, stage = 'setup'
  const browserErrors = [], completed = [], conversations = [], candidates = [], primaries = []
  const cleanupErrors = []
  const cleanup = async (action) => {
    try { await action() } catch (error) { cleanupErrors.push(error); log(`Cleanup: ${safeError(error)}`) }
  }
  const openBrowser = async () => {
    chromium ??= (await import('@playwright/test')).chromium
    browser = await chromium.launch({ timeout: 15000 })
    page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    if (fullUI) page.setDefaultTimeout(15000)
    page.on('pageerror', (error) => browserErrors.push(fullUI ? 'pageerror' : error.message))
    page.on('requestfailed', (req) => {
      if (fullUI && req.failure()?.errorText === 'net::ERR_ABORTED'
        && new URL(req.url()).pathname.startsWith('/api/v1/')) return // Navigation aborts old API/SSE reads.
      browserErrors.push(fullUI ? 'requestfailed' : `${req.url()}: ${req.failure()?.errorText}`)
    })
    page.on('response', (response) => { if (!response.ok() && response.status() !== 304) browserErrors.push(fullUI ? `HTTP_${response.status()}` : `HTTP ${response.status()}: ${response.url()}`) })
  }
  const uiWait = async (predicate, value, description) => {
    try { await page.waitForFunction(predicate, value, { timeout: 15000 }) }
    catch { throw new Error(description) }
  }
  const uiResponse = (path) => page.waitForResponse((response) => new URL(response.url()).pathname === path && response.request().method() === 'POST', { timeout: 15000 })
  const newConversation = async () => {
    const created = uiResponse(`/api/v1/projects/${project.id}/conversations`)
    await page.getByRole('button', { name: '新建会话', exact: true }).click()
    const response = await created, value = await response.json()
    assert(uuid(value.id), 'invalid Conversation ID')
    conversations.push(value.id)
    assert.equal(response.status(), 201, 'Conversation creation failed')
    await page.getByRole('textbox', { name: '消息内容' }).waitFor({ state: 'visible' })
    return value
  }
  const timeline = (ids) => uiWait((expected) => JSON.stringify([...document.querySelectorAll('[data-run-id]')].map((element) => element.getAttribute('data-run-id'))) === JSON.stringify(expected), ids, 'UI timeline mismatch')
  const showReport = async (artifact) => {
    await uiWait((url) => document.querySelector('#artifact-pane iframe')?.getAttribute('src') === url, artifact.gateway_url, 'UI artifact version mismatch')
    const handle = await page.locator('#artifact-pane iframe').elementHandle()
    const frame = await handle?.contentFrame()
    assert(frame, 'missing report iframe')
    try {
      await frame.waitForFunction(() => [...document.querySelectorAll('[_echarts_instance_]')].some((element) => {
        const chart = window.echarts?.getInstanceByDom?.(element)
        const box = element.getBoundingClientRect(), surface = element.querySelector('canvas,svg')?.getBoundingClientRect()
        return chart?.getOption()?.series?.length > 0
          && chart.getWidth() > 0 && chart.getHeight() > 0 && box.width > 0 && box.height > 0
          && surface?.width > 0 && surface?.height > 0 && chart.getZr()?.storage?.getDisplayList()?.length > 0
      }), {}, { timeout: 15000 })
    } catch { throw new Error('report ECharts instance did not render') }
    assert.equal(browserErrors.length, 0, browserErrors.join('\n'))
  }
  const replay = async (ids) => {
    for (const id of ids) {
      const events = await request(`/api/v1/runs/${id}/events`)
      const expected = events.map(({ sequence, type }) => ({ sequence, type }))
      assert(expected.every((event, index) => event.sequence === index + 1), 'durable event sequence has gaps')
      await uiWait(({ id, expected }) => {
        const app = document.querySelector('#app')?.__vue_app__
        const actual = app?.config.globalProperties.$pinia._s.get('runs')?.events.get(id)?.map(({ sequence, type }) => ({ sequence, type }))
        return JSON.stringify(actual) === JSON.stringify(expected)
      }, { id, expected }, 'UI replay mismatch')
    }
  }
  try {
    csv ??= await readFile(new URL('../../../tests/fixtures/geo/locations.csv', import.meta.url))
    const digest = createHash('sha256').update(csv).digest('hex')
    const rowCount = csv.toString('utf8').trim().split(/\r?\n/).length - 1
    let conversation
    if (fullUI) {
      assert.equal(rowCount, 4, 'full UI requires the four-row Geo fixture')
      await openBrowser()
      stage = 'project'
      const response = await page.goto(webURL, { waitUntil: 'domcontentloaded', timeout: 15000 })
      assert(response?.ok(), 'workbench failed to load')
      await page.getByRole('button', { name: '新建项目', exact: true }).click()
      await page.locator('dialog input').fill('Geo OpenAI smoke')
      const created = uiResponse('/api/v1/projects')
      await page.locator('dialog button[type=submit]').click()
      const accepted = await created
      project = await accepted.json() // Capture ownership before any later UI assertion.
      assert(uuid(project.id), 'invalid Project ID')
      assert.equal(accepted.status(), 201, 'Project creation failed')
      await page.getByRole('button', { name: '新建会话', exact: true }).waitFor({ state: 'visible' })
      stage = 'upload'
      await page.locator('details.input-files summary').click()
      const uploading = uiResponse(`/api/v1/projects/${project.id}/inputs`)
      await page.locator('input[type=file]').setInputFiles({ name: 'locations.csv', mimeType: 'text/csv', buffer: csv })
      const uploaded = await uploading
      assert.equal(uploaded.status(), 201, 'Input upload failed')
      assert.equal((await uploaded.json()).sha256_digest, digest, 'uploaded input digest mismatch')
      conversation = await newConversation()
    } else {
      project = await post('/api/v1/projects', { name: 'Geo Claude smoke', profile_id: 'geo-analysis' })
      const form = new FormData()
      form.set('file', new Blob([csv], { type: 'text/csv' }), 'locations.csv')
      const uploaded = await request(`/api/v1/projects/${project.id}/inputs`, { method: 'POST', body: form })
      assert.equal(uploaded.sha256_digest, digest, 'uploaded input digest mismatch')
      conversation = await post(`/api/v1/projects/${project.id}/conversations`, { title: 'Geo smoke' })
    }
    for (let turn = 0; turn < (fullUI ? 3 : 1); turn++) {
      stage = 'submit'
      if (fullUI) {
        if (turn === 2) conversation = await newConversation()
        await page.getByRole('textbox', { name: '消息内容' }).fill(`Analysis run ${turn + 1}. ${fullPrompt}`)
        const submitted = uiResponse(`/api/v1/conversations/${conversation.id}/messages`)
        await page.locator('#chat-pane button[type=submit]').click()
        const response = await submitted
        ;({ run } = await response.json())
        assert(uuid(run?.id), 'invalid Run ID')
        assert.equal(response.status(), 201, 'Run submission failed')
      } else {
        ;({ run } = await post(`/api/v1/conversations/${conversation.id}/messages`, { content: prompt }))
      }
      stage = 'run'
    const deadline = now() + 120000
    while (true) {
      if (now() >= deadline) throw new Error('Run timed out after 120 seconds')
      run = await request(`/api/v1/runs/${run.id}`, {}, deadline)
      if (terminal(run) && (!fullUI || run.status !== 'succeeded' || run.finalized_at)) break
      await sleep(Math.min(1000, Math.max(0, deadline - now())))
    }
    assert.equal(run.status, 'succeeded', `Run ${run.status}`)
    if (fullUI) {
      assert(run.conversation_id === conversation.id, 'Run belongs to another Conversation')
      assert(uuid(run.candidate_sdk_session_id) && !candidates.includes(run.candidate_sdk_session_id), 'invalid or repeated SDK candidate')
      assert(run.source_sdk_session_id === (turn === 1 ? candidates[0] : null), 'SDK source lineage mismatch')
      assert((await request(`/api/v1/conversations/${conversation.id}`)).active_sdk_session_id === run.candidate_sdk_session_id, 'Conversation active SDK Session mismatch')
      candidates.push(run.candidate_sdk_session_id)
      log(`Run: ${JSON.stringify(safeRun(run))}`)
    }
    stage = 'evidence'
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
    if (fullUI) {
      assert(evidence.inputs.some((input) => input.input_digest === digest && input.row_count === 4 && input.computed_fields?.value_sum === 54), 'incorrect value_sum analysis evidence')
      assert(uuid(primary[0].id) && primary[0].run_id === run.id, 'invalid primary Artifact ownership')
      assert(primary[0].entry_path === 'report/index.html', 'unexpected primary entry path')
      completed.push(run.id); primaries.push(primary[0])
      stage = 'chart'
      await showReport(primary[0])
      await timeline(turn < 2 ? completed : [run.id])
      await replay(turn < 2 ? completed : [run.id])
      if (turn === 1) {
        stage = 'versions'
        await uiWait((ids) => JSON.stringify([...document.querySelectorAll('select[aria-label="制品版本"] option')].map((option) => option.value).sort()) === JSON.stringify([...ids].sort()), completed, 'UI report versions mismatch')
        for (const index of [0, 1, 0]) {
          await page.getByLabel('制品版本').selectOption(completed[index])
          await showReport(primaries[index])
        }
        stage = 'reload'
        await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 })
        await showReport(primaries[0])
        await timeline(completed)
        await replay(completed)
      }
    } else {
      await openBrowser()
    const response = await page.goto(primary[0].gateway_url, { waitUntil: 'networkidle', timeout: 15000 })
    assert(response?.ok(), 'primary report failed to load')
    await page.waitForFunction(() => document.body?.textContent?.trim() && window.echarts, { }, { timeout: 15000 })
    assert.equal(browserErrors.length, 0, browserErrors.join('\n'))
    }
    }
    if (fullUI) {
      stage = 'isolation'
      await page.locator(`a[href="/projects/${project.id}/conversations/${conversations[0]}"]`).click()
      await timeline(completed.slice(0, 2))
      await replay(completed.slice(0, 2))
      await page.getByLabel('制品版本').selectOption(completed[1])
      await showReport(primaries[1])
      await page.screenshot({ path: join(tmpdir(), `hf-openai-smoke-${run.id}.png`), fullPage: true, timeout: 15000 })
    }
    log(`Smoke passed: Run ${uuid(run.id) ? run.id : 'unknown'}`)
    if (fullUI) return { project_id: project.id, conversation_ids: conversations, run_ids: completed, artifact_ids: primaries.map((artifact) => artifact.id) }
  } catch (error) {
    failure = error
    log(`Smoke: ${JSON.stringify({ stage })}`)
    if (fullUI && page && uuid(run?.id)) {
      try { await page.screenshot({ path: join(tmpdir(), `hf-openai-smoke-${run.id}-failed.png`), fullPage: true, timeout: 15000 }) }
      catch { /* Preserve the execution failure if the browser is unavailable. */ }
    }
    if (run) {
      try { run = await request(`/api/v1/runs/${run.id}`) } catch (diagnostic) { log(`Run diagnostic: ${safeError(diagnostic)}`) }
      log(`Run: ${JSON.stringify(safeRun(run))}`)
      try { log(`Events: ${JSON.stringify(safeEvents(await request(`/api/v1/runs/${run.id}/events`)))}`) }
      catch (diagnostic) { log(`Events: unavailable (${safeError(diagnostic)})`) }
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
    await runSmoke().catch((error) => { console.error(safeError(error)); process.exitCode = 1 })
  }
}
