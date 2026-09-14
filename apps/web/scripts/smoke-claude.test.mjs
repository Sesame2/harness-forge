import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
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
  const calls = [], logs = [], browserEvents = {}
  const run = () => ({ id: 'run-1', status: cancelled ? 'cancelled' : (options.status ?? 'succeeded'), finalized_at: cancelled ? (options.neverFinalize ? null : 'now') : (options.status === 'running' || options.unfinalized ? null : 'now') })
  const fetch = async (url, init = {}) => {
    const path = new URL(url).pathname
    const method = init.method ?? 'GET'
    calls.push([method, path, init])
    let body
    if (method === 'DELETE') return new Response(null, { status: options.deleteConflict ? 409 : 204 })
    if (path.endsWith('/cancel')) { cancelled = true; body = run() }
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

test('terminal but unfinalized Run also uses cancel/finalized cleanup', async () => {
  const f = fixture({ status: 'failed', unfinalized: true })
  await assert.rejects(runSmoke(f.args), /Run failed/)
  assert(f.calls.some(([, path]) => path?.endsWith('/cancel')))
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
