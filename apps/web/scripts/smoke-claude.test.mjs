import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { runSmoke } from './smoke-claude.mjs'

const csv = Buffer.from('name,latitude,longitude,value\nA,31,121,10\nB,30,120,20\n')
const digest = createHash('sha256').update(csv).digest('hex')
const events = [
  { type: 'assistant.message', payload: { text: 'Inspecting data' } },
  { type: 'tool.started', payload: { tool_call_id: 'python-1', name: 'Bash', input: { command: "python - <<'PY'\nprint(1)\nPY" } } },
  { type: 'tool.completed', payload: { tool_call_id: 'python-1', name: 'Bash', outcome: 'succeeded', output: '1' } },
]

function fixture(options = {}) {
  let time = 0
  let cancelled = false
  let wonByCompletion = false
  const calls = [], logs = [], browserEvents = {}
  const run = () => ({ id: 'run-1', status: wonByCompletion ? 'succeeded' : cancelled ? 'cancelled' : (options.status ?? 'succeeded'), finalized_at: options.finalizeAfter !== undefined && time >= options.finalizeAfter ? 'now' : cancelled ? (options.neverFinalize ? null : 'now') : (options.status === 'running' || options.unfinalized ? null : 'now'), ...options.runExtras })
  const fetch = async (url, init = {}) => {
    const path = new URL(url).pathname
    const method = init.method ?? 'GET'
    calls.push([method, path, init])
    let body
    if (method === 'DELETE') return new Response(null, { status: options.deleteConflict ? 409 : 204 })
    if (path.endsWith('/cancel')) {
      if (options.completionWinsCancel) wonByCompletion = true
      if (options.cancelConflict || ['succeeded', 'failed', 'interrupted'].includes(run().status)) return new Response(null, { status: 409 })
      cancelled = true; body = run()
    }
    else if (path === '/api/v1/projects') body = { id: 'project-1' }
    else if (path.endsWith('/inputs')) {
      assert.equal(init.body.get('file').name, 'locations.csv')
      assert.equal(init.body.get('file').type, 'text/csv')
      body = { sha256_digest: digest }
    } else if (path.endsWith('/conversations')) body = { id: 'conversation-1' }
    else if (path.endsWith('/messages')) body = { message: { id: 'message-1' }, run: { id: 'run-1', status: 'queued', finalized_at: null } }
    else if (path.endsWith('/events')) body = options.events ?? events
    else if (path.endsWith('/artifacts')) body = options.artifacts ?? [
      { id: 'report-1', type: 'html', entry_path: 'report/index.html', is_primary: true, gateway_url: 'http://localhost:8081/artifacts/report-1/report/index.html' },
      { id: 'evidence-1', type: 'data', entry_path: 'data/analysis-evidence.json', is_primary: false, gateway_url: 'http://localhost:8081/artifacts/evidence-1/data/analysis-evidence.json' },
    ]
    else if (path.endsWith('/analysis-evidence.json')) body = options.evidence ?? { inputs: [{ input_digest: digest, row_count: 2, computed_fields: { value_sum: 30 } }] }
    else if (path === '/api/v1/runs/run-1') body = run()
    else throw new Error(`unexpected request ${method} ${path}`)
    return Response.json(body)
  }
  const page = {
    on(name, handler) { browserEvents[name] = handler },
    async goto(url) {
      assert.match(url, /report\/index.html$/)
      if (options.pageerror) browserEvents.pageerror(new Error('chart crashed'))
      if (options.requestfailed) browserEvents.requestfailed({ url: () => '/vendor/echarts.min.js', failure: () => ({ errorText: 'net::ERR_FAILED' }) })
      return { ok: () => true }
    },
    async waitForFunction() { if (options.emptyPage) throw new Error('empty page or missing echarts') },
  }
  const browser = { async newPage() { return page }, async close() { calls.push(['CLOSE']) } }
  return {
    calls, logs, elapsed: () => time,
    args: { fetch, chromium: { async launch() { return browser } }, csv, now: () => time,
      sleep: async (ms) => { time += ms }, log: (...args) => logs.push(args.join(' ')) },
  }
}

test('successful Geo smoke follows real API fields, checks evidence/browser and deletes project', async () => {
  const f = fixture()
  await runSmoke(f.args)
  assert(f.calls.some(([method, path]) => method === 'DELETE' && path.endsWith('project-1')))
  assert(!f.calls.some(([, path]) => path?.endsWith('/cancel')))
  assert(f.calls.some(([method]) => method === 'CLOSE'))
})

test('failed Run prints diagnostics and preserves failure despite delete conflict', async () => {
  const f = fixture({ status: 'failed', deleteConflict: true })
  await assert.rejects(runSmoke(f.args), /Run failed/)
  assert(f.logs.some((line) => line.includes('Events:')))
  assert(f.logs.some((line) => line.includes('Run:')))
  assert(f.logs.some((line) => line.includes('409')))
})

test('120 second timeout cancels, waits for finalized, then deletes', async () => {
  const f = fixture({ status: 'running' })
  await assert.rejects(runSmoke(f.args), /120 seconds/)
  assert.equal(f.elapsed(), 120000)
  const cancel = f.calls.findIndex(([, path]) => path?.endsWith('/cancel'))
  const deletion = f.calls.findIndex(([method]) => method === 'DELETE')
  assert(cancel > 0 && deletion > cancel)
  assert(f.calls.slice(cancel + 1, deletion).some(([method, path]) => method === 'GET' && path.endsWith('/run-1')))
})

test('terminal failed Run waits for finalization without unsupported cancellation', async () => {
  const f = fixture({ status: 'failed', unfinalized: true, finalizeAfter: 1000 })
  await assert.rejects(runSmoke(f.args), /Run failed/)
  assert(!f.calls.some(([, path]) => path?.endsWith('/cancel')))
  assert.equal(f.elapsed(), 1000)
  assert(!f.logs.some((line) => line.includes('Cleanup:')))
})

test('succeeded but unfinalized Run completes cleanup without calling cancel (which returns 409)', async () => {
  const f = fixture({ unfinalized: true, finalizeAfter: 1000 })
  await runSmoke(f.args)
  assert(!f.calls.some(([, path]) => path?.endsWith('/cancel')))
  assert.equal(f.elapsed(), 1000)
  assert(f.calls.some(([method]) => method === 'DELETE'))
})

test('completion racing cancellation refreshes 409 terminal state and waits for finalization', async () => {
  const f = fixture({ status: 'running', completionWinsCancel: true, finalizeAfter: 121000 })
  await assert.rejects(runSmoke(f.args), /120 seconds/)
  assert.equal(f.elapsed(), 121000)
  assert(!f.logs.some((line) => line.includes('Cleanup:')))
  assert(f.calls.some(([method]) => method === 'DELETE'))
})

test('409 cancellation still reporting nonterminal state is not swallowed', async () => {
  const f = fixture({ status: 'running', cancelConflict: true })
  await assert.rejects(runSmoke(f.args), /120 seconds/)
  assert(f.logs.some((line) => line.includes('Cleanup:') && line.includes('409')))
  assert(f.calls.some(([method]) => method === 'DELETE'))
})

test('cleanup finalization timeout is bounded, still attempts delete and preserves original error', async () => {
  const f = fixture({ status: 'running', neverFinalize: true })
  await assert.rejects(runSmoke(f.args), /120 seconds/)
  assert.equal(f.elapsed(), 150000)
  assert(f.logs.some((line) => line.includes('finalized')))
  assert(f.calls.some(([method]) => method === 'DELETE'))
})

for (const issue of ['pageerror', 'requestfailed', 'emptyPage']) {
  test(`browser ${issue} fails and cleans up`, async () => {
    const f = fixture({ [issue]: true })
    await assert.rejects(runSmoke(f.args), /chart crashed|ERR_FAILED|empty page/)
    assert(f.calls.some(([method]) => method === 'CLOSE'))
    assert(f.calls.some(([method]) => method === 'DELETE'))
  })
}

test('successful Run without paired successful Python execution fails', async () => {
  const f = fixture({ events: events.slice(0, 2) })
  await assert.rejects(runSmoke(f.args), /Python/)
})

test('wrong evidence digest or missing computed fields fails', async () => {
  const f = fixture({ evidence: { inputs: [{ input_digest: 'invented', row_count: 2, computed_fields: {} }] } })
  await assert.rejects(runSmoke(f.args), /evidence/)
})

test('multiple primary artifacts fail', async () => {
  const f = fixture({ artifacts: [{ is_primary: true }, { is_primary: true }] })
  await assert.rejects(runSmoke(f.args), /one primary/)
})

test('cleanup failure turns otherwise successful smoke into failure', async () => {
  const f = fixture({ deleteConflict: true })
  await assert.rejects(runSmoke(f.args), /409/)
})

test('Make smoke holds docker provider through cleanup and preserves original failure status', async () => {
  const temporary = await mkdtemp(join(tmpdir(), 'hf-smoke-make-'))
  const trace = join(temporary, 'trace')
  try {
    // Only boundary commands are stubbed; the actual Make recipe is executed.
    for (const [name, script] of Object.entries({
      docker: '#!/bin/sh\necho "docker:$SANDBOX_PROVIDER:$*" >> "$TRACE"\n',
      pnpm: '#!/bin/sh\nexit 7\n',
      'cleanup-make': '#!/bin/sh\necho "cleanup:$SANDBOX_PROVIDER:$*" >> "$TRACE"\nexit 9\n',
    })) await writeFile(join(temporary, name), script, { mode: 0o755 })
    const result = spawnSync('/usr/bin/make', ['smoke-claude', `MAKE=${join(temporary, 'cleanup-make')}`], {
      cwd: new URL('../../../', import.meta.url), encoding: 'utf8',
      env: { ...process.env, PATH: `${temporary}:/usr/bin:/bin`, TRACE: trace, ANTHROPIC_API_KEY: 'fixture-only', SANDBOX_PROVIDER: 'fake' },
    })
    assert.notEqual(result.status, 0)
    assert.match(result.stderr, /Error 7/)
    const recorded = await readFile(trace, 'utf8')
    assert.match(recorded, /docker:docker:compose -f docker-compose.yaml up/)
    assert.match(recorded, /cleanup:docker:purge-deleted/)
    assert.match(recorded, /logs --tail=200 control-plane agent-runtime/)
    const empty = spawnSync('/usr/bin/make', ['smoke-claude'], {
      cwd: new URL('../../../', import.meta.url), encoding: 'utf8',
      env: { PATH: '/nonexistent', ANTHROPIC_API_KEY: '' },
    })
    assert.notEqual(empty.status, 0)
    assert.match(empty.stdout, /ANTHROPIC_API_KEY is required/)
    assert.doesNotMatch(empty.stderr, /docker|pnpm/)
  } finally { await rm(temporary, { recursive: true, force: true }) }
})

test('echoing the word python is not evidence of Python execution', async () => {
  const f = fixture({ events: [events[0], { ...events[1], payload: { ...events[1].payload, input: { command: 'echo python' } } }, events[2]] })
  await assert.rejects(runSmoke(f.args), /Python/)
})

test('failure diagnostics never include private Run metadata, error messages or event payloads', async () => {
  const f = fixture({ status: 'failed', runExtras: { prompt: 'PRIVATE_SENTINEL', error: { code: 'worker_failed', message: 'PRIVATE_SENTINEL' } },
    events: [{ sequence: 1, type: 'agent.failed', payload: { command: 'PRIVATE_SENTINEL', message: 'PRIVATE_SENTINEL' } }] })
  await assert.rejects(runSmoke(f.args), /Run failed/)
  assert(f.logs.some((line) => line.startsWith('Run:') && line.includes('worker_failed')))
  assert(f.logs.some((line) => line.startsWith('Events:') && line.includes('agent.failed')))
  assert.doesNotMatch(f.logs.join('\n'), /PRIVATE_SENTINEL|"payload"|"prompt"|"command"/)
})

const uuid = (number) => `00000000-0000-4000-8000-${String(number).padStart(12, '0')}`
const fullCSV = await readFile(new URL('../../../tests/fixtures/geo/locations.csv', import.meta.url))
const fullDigest = createHash('sha256').update(fullCSV).digest('hex')

function uiFixture(options = {}) {
  let time = 0, currentConversation, selected, reloaded = false, waiter, text = ''
  const calls = [], logs = [], conversations = [], runs = [], screenshots = [], browserEvents = {}
  const eventLog = (id) => events.map((event, index) => ({ ...event, run_id: id, sequence: index + 1 }))
  const visibleRuns = () => runs.filter((run) => run.conversation_id === currentConversation)
  const selectedRun = () => selected ?? visibleRuns().at(-1)?.id
  const reportURL = (id) => `http://localhost:8081/artifacts/${uuid(100 + runs.findIndex((run) => run.id === id))}/report/index.html`
  const createRun = () => {
    const index = runs.length
    assert(runs.every((run) => run.finalized_at), 'next run submitted before prior finalization')
    const value = { id: uuid(20 + index), conversation_id: currentConversation, status: 'queued', phase: 'agent', finalized_at: null,
      candidate_sdk_session_id: uuid(30 + index), source_sdk_session_id: index === 1 ? runs[0].candidate_sdk_session_id : null }
    if (options.wrongSource && index === 1) value.source_sdk_session_id = uuid(999)
    runs.push(value)
    selected = undefined
    return { run: { ...value }, message: { id: uuid(50 + index) } }
  }
  const mutate = (path, kind) => {
    let body
    if (kind === 'project') body = { id: uuid(1) }
    if (kind === 'input') body = { id: uuid(2), sha256_digest: fullDigest }
    if (kind === 'conversation') {
      const conversation = { id: uuid(10 + conversations.length), active_sdk_session_id: null }
      conversations.push(conversation); currentConversation = conversation.id; selected = undefined; body = conversation
    }
    if (kind === 'message') body = createRun()
    calls.push(['UI_POST', path])
    const response = { status: () => 201, json: async () => structuredClone(body), url: () => `http://localhost:5173${path}`, request: () => ({ method: () => 'POST' }) }
    assert(waiter?.predicate(response), `unexpected UI response ${path}`)
    waiter.resolve(response); waiter = undefined
  }
  const locate = (selector) => ({
    async fill(value) { calls.push(['FILL', selector, value]); text = value },
    async click() {
      calls.push(['CLICK', selector])
      if (selector === 'dialog button[type=submit]') mutate('/api/v1/projects', 'project')
      if (selector === 'button:新建会话') mutate(`/api/v1/projects/${uuid(1)}/conversations`, 'conversation')
      if (selector === '#chat-pane button[type=submit]') {
        assert(!text.includes('54'), 'prompt must not disclose expected calculation')
        mutate(`/api/v1/conversations/${currentConversation}/messages`, 'message')
      }
      if (selector.startsWith('a[href=')) { currentConversation = selector.match(/conversations\/([^"]+)/)[1]; selected = undefined }
    },
    async setInputFiles(file) {
      assert.deepEqual(file.buffer, fullCSV)
      assert.equal(file.name, 'locations.csv')
      mutate(`/api/v1/projects/${uuid(1)}/inputs`, 'input')
    },
    async waitFor() { if (options.projectUIFailure && selector === 'button:新建会话') throw new Error('PRIVATE_SENTINEL') },
    async selectOption(id) { calls.push(['SELECT', id]); if (!options.wrongVersion) selected = id },
    async getAttribute() { return reportURL(selectedRun()) },
    async elementHandle() { return { contentFrame: async () => frame } },
  })
  const chartElement = { getBoundingClientRect: () => ({ width: 640, height: 480 }), querySelector: () => ({ getBoundingClientRect: () => ({ width: 640, height: 480 }) }) }
  const chart = { getOption: () => ({ series: [options.datasetChart ? { type: 'bar', datasetIndex: 0 } : { data: [12, 18, 9, 15] }] }), getWidth: () => 640, getHeight: () => 480,
    getZr: () => ({ storage: { getDisplayList: () => ['painted'] } }) }
  const frame = {
    async waitForFunction(fn, arg) {
      const okay = runInNewContext(`(${fn.toString()})(arg)`, { arg, window: { echarts: options.chartStub ? {} : { getInstanceByDom: () => chart } },
        document: { body: { textContent: 'Report' }, querySelectorAll: () => [chartElement] } })
      if (!okay) throw new Error('fixture chart did not render')
    },
  }
  const page = {
    on(name, handler) { browserEvents[name] = handler },
    setDefaultTimeout() {},
    async goto(url) { calls.push(['GOTO', url]); return { ok: () => true } },
    locator: locate,
    getByRole: (role, { name }) => locate(`${role}:${name}`),
    getByLabel: (name) => locate(`label:${name}`),
    waitForResponse(predicate) { assert(!waiter); return new Promise((resolve) => { waiter = { predicate, resolve } }) },
    async waitForFunction(fn, arg) {
      const applied = new Map(visibleRuns().map((run) => [run.id, eventLog(run.id).slice(options.replayGap && reloaded ? 1 : 0)]))
      const doc = {
        querySelector(selector) {
          if (selector === '#app') return { __vue_app__: { config: { globalProperties: { $pinia: { _s: new Map([['runs', { events: applied }]]) } } } } }
          if (selector === '#artifact-pane iframe') return { getAttribute: () => reportURL(selectedRun()) }
          return null
        },
        querySelectorAll(selector) {
          if (selector === '[data-run-id]') return visibleRuns().map((run) => ({ getAttribute: () => run.id }))
          if (selector.includes('option')) return visibleRuns().map((run) => ({ value: run.id }))
          return []
        },
      }
      if (!runInNewContext(`(${fn.toString()})(arg)`, { arg, document: doc })) throw new Error('fixture UI condition failed')
    },
    async reload() { reloaded = true; if (options.refreshVersion) selected = undefined; calls.push(['RELOAD']) },
    async screenshot({ path }) { screenshots.push(path) },
  }
  const fetch = async (url, init = {}) => {
    const path = new URL(url).pathname, method = init.method ?? 'GET'
    calls.push([method, path])
    if (method === 'DELETE') return new Response(null, { status: 204 })
    assert.equal(method, 'GET', 'full UI mutations must pass through actual browser controls')
    let body
    const run = runs.find((run) => path.includes(run.id))
    if (path.endsWith('/events')) body = eventLog(run.id)
    else if (path.endsWith('/artifacts')) body = [
      { id: uuid(100 + runs.indexOf(run)), run_id: run.id, is_primary: true, type: 'html', entry_path: 'report/index.html', gateway_url: reportURL(run.id) },
      { id: uuid(200 + runs.indexOf(run)), run_id: run.id, is_primary: false, type: 'data', entry_path: 'data/analysis-evidence.json', gateway_url: `http://localhost:8081/artifacts/${uuid(200 + runs.indexOf(run))}/data/analysis-evidence.json` },
    ]
    else if (path.endsWith('/analysis-evidence.json')) body = { inputs: [{ input_digest: fullDigest, row_count: 4, computed_fields: { value_sum: options.wrongSum ? 0 : 54 } }] }
    else if (run) {
      run.status = options.failureAt === runs.indexOf(run) ? 'failed' : 'succeeded'
      if (time > runs.indexOf(run) * 1000) run.finalized_at = '2026-10-02T10:00:00Z'
      if (run.finalized_at && run.status === 'succeeded') conversations.find((item) => item.id === run.conversation_id).active_sdk_session_id = run.candidate_sdk_session_id
      body = run
    } else if (path.includes('/conversations/')) body = conversations.find((item) => path.endsWith(item.id))
    else throw new Error(`unexpected fixture GET ${path}`)
    return Response.json(body)
  }
  return { calls, logs, runs, conversations, screenshots, args: { fullUI: true, webURL: 'http://localhost:5173', fetch, csv: fullCSV,
    chromium: { launch: async () => ({ newPage: async () => page, close: async () => calls.push(['CLOSE']) }) },
    now: () => time, sleep: async (ms) => { time += ms }, log: (line) => logs.push(line) } }
}

test('full UI smoke creates, uploads and sends three runs through controls, verifies lineage and replay', async () => {
  const f = uiFixture()
  const result = await runSmoke(f.args)
  assert.deepEqual(result, { project_id: uuid(1), conversation_ids: [uuid(10), uuid(11)], run_ids: [uuid(20), uuid(21), uuid(22)], artifact_ids: [uuid(100), uuid(101), uuid(102)] })
  assert.equal(f.calls.filter(([method, path]) => method === 'UI_POST' && path.endsWith('/messages')).length, 3)
  assert(f.calls.some(([method]) => method === 'RELOAD'))
  assert.deepEqual(f.calls.filter(([method]) => method === 'SELECT').slice(0, 3).map(([, id]) => id), [uuid(20), uuid(21), uuid(20)])
  assert.equal(f.screenshots.length, 1)
  assert(f.screenshots[0].endsWith(`hf-openai-smoke-${uuid(22)}.png`))
  assert(f.calls.some(([method, path]) => method === 'DELETE' && path.endsWith(uuid(1))))
})

test('full UI accepts a rendered dataset-based ECharts report', async () => {
  const f = uiFixture({ datasetChart: true })
  assert.equal((await runSmoke(f.args)).run_ids.length, 3)
})

for (const [option, count] of [['failureAt', 2], ['wrongSource', 2], ['wrongSum', 1], ['chartStub', 1], ['wrongVersion', 2], ['refreshVersion', 2], ['replayGap', 2]]) {
  test(`full UI ${option} fails before unnecessary paid runs and still deletes project`, async () => {
    const f = uiFixture({ [option]: option === 'failureAt' ? 1 : true })
    await assert.rejects(runSmoke(f.args))
    assert.equal(f.runs.length, count)
    assert(f.calls.some(([method, path]) => method === 'DELETE' && path.endsWith(uuid(1))))
    assert(!f.logs.some((line) => line.includes('Smoke passed')))
  })
}

test('project ID is retained for cleanup if a later UI assertion fails', async () => {
  const f = uiFixture({ projectUIFailure: true })
  await assert.rejects(runSmoke(f.args))
  assert(f.calls.some(([method, path]) => method === 'DELETE' && path.endsWith(uuid(1))))
  assert.doesNotMatch(f.logs.join('\n'), /PRIVATE_SENTINEL/)
})
