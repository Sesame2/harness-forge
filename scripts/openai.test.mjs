import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

// Importing a launcher must not read .env, start Docker, or make a model request.
import { runOpenAI } from './openai.mjs'
const root = fileURLToPath(new URL('../', import.meta.url)).replace(/\/$/, '')
const dotenv = 'OPENAI_API_KEY=fixture-secret\nOPENAI_BASE_URL=https://API.EXAMPLE:443/\nOPENAI_CHAT_MODEL=chat-fixture\nOPENAI_RESPONSES_MODEL=responses-fixture\nUNRELATED_SECRET=do-not-forward\n'
const id = (number) => `00000000-0000-4000-8000-${String(number).padStart(12, '0')}`
const evidence = { project_id: id(1), conversation_ids: [id(2), id(3)], run_ids: [id(4), id(5), id(6)], artifact_ids: [id(7), id(8), id(9)] }

function fixture(overrides = {}) {
  const calls = [], logs = [], smoke = [], reads = []
  return { calls, logs, smoke, reads, options: {
    env: { PATH: '/fixture/bin', HOME: '/fixture/home', DOCKER_CONTEXT: 'fixture-context',
      COMPOSE_FILE: '/foreign.yml', COMPOSE_ENV_FILES: '/foreign.env', COMPOSE_PROJECT_NAME: 'foreign',
      OPENAI_API_KEY: 'foreign-key', ANTHROPIC_API_KEY: 'foreign-claude',
      DATABASE_URL: 'foreign-db', MINIO_ENDPOINT: 'http://foreign', SANDBOX_PROVIDER: 'fake',
      HF_GATEWAY_KEY: 'foreign-gateway', NODE_OPTIONS: '--require=/foreign.js' },
    readFile: async (path) => { reads.push(path); return dotenv },
    fetch: async (url, init) => { calls.push({ command: 'fetch', url, init }); return Response.json({
      status: 'completed', choices: [{ message: { content: 'fixture-answer' } }],
      output: [{ type: 'message', content: [{ type: 'output_text', text: 'fixture-answer' }] }],
    }) },
    run: async (command, args, options) => { calls.push({ command, args, options }); return command === process.execPath ? JSON.stringify(evidence) : '' },
    smokeRunner: async (options) => { smoke.push(options); return evidence },
    log: (line) => logs.push(line), ...overrides,
  } }
}

test('launcher entry point exists', () => assert.equal(typeof runOpenAI, 'function'))

for (const api of ['chat', 'responses']) {
  test(`${api} check uses exactly the selected endpoint/model, bounded output and safe summary`, async () => {
    const f = fixture()
    await runOpenAI(['check', '--api', api, '--env-file', '/fixture/config.env'], f.options)
    assert.deepEqual(f.reads, ['/fixture/config.env'])
    assert.equal(f.calls.length, 1)
    const { command, url, init } = f.calls[0]
    assert.equal(command, 'fetch')
    assert.equal(url, `https://api.example/v1/${api === 'chat' ? 'chat/completions' : 'responses'}`)
    assert.equal(init.headers.Authorization, 'Bearer fixture-secret')
    assert.equal(init.redirect, 'error')
    assert(init.signal instanceof AbortSignal)
    const body = JSON.parse(init.body)
    assert.equal(body.model, `${api}-fixture`)
    assert.equal(body.store, false)
    assert.equal(body.stream, false)
    assert.equal(body[api === 'chat' ? 'max_completion_tokens' : 'max_output_tokens'], 64)
    assert.match(f.logs.join(' '), new RegExp(`mode=${api} status=200 model_sha256=${createHash('sha256').update(`${api}-fixture`).digest('hex').slice(0, 12)}`))
    assert.doesNotMatch(f.logs.join(' '), /fixture-secret|fixture-answer|api\.example|chat-fixture|responses-fixture/)
  })
}

test('existing URL prefix and dotenv literal values are preserved; only selected model is required', async () => {
  const f = fixture({ readFile: async () => 'OPENAI_API_KEY="literal-$(touch /tmp/no)-key"\nOPENAI_BASE_URL=https://api.example/proxy/openai///\nOPENAI_RESPONSES_MODEL=only-responses\n' })
  await runOpenAI(['check', '--api', 'responses'], f.options)
  assert.equal(f.calls[0].url, 'https://api.example/proxy/openai/responses')
  assert.equal(f.calls[0].init.headers.Authorization, 'Bearer literal-$(touch /tmp/no)-key')
})

for (const value of ['', 'http://api.example', 'https://u:p@api.example', 'https://api.example?', 'https://api.example#', 'https://api.example/a\\b', 'https://api.example/a b']) {
  test(`invalid upstream URL is rejected before external I/O: ${JSON.stringify(value)}`, async () => {
    const f = fixture({ readFile: async () => dotenv.replace('https://API.EXAMPLE:443/', value ? `"${value}"` : '') })
    await assert.rejects(runOpenAI(['smoke', '--api', 'chat'], f.options), { message: 'HF_OPENAI_CONFIG' })
    assert.deepEqual(f.calls, [])
  })
}

for (const field of ['OPENAI_API_KEY', 'OPENAI_CHAT_MODEL']) {
  test(`empty ${field} rejects before Docker/HTTP and cannot use inherited credentials`, async () => {
    const f = fixture({ readFile: async () => dotenv.replace(new RegExp(`${field}=.*`), `${field}=`) })
    await assert.rejects(runOpenAI(['smoke', '--api', 'chat'], f.options), { message: 'HF_OPENAI_CONFIG' })
    assert.deepEqual(f.calls, [])
  })
}

for (const args of [[], ['unknown'], ['check'], ['check', '--api', 'other'], ['check', '--api', 'chat', '--unknown'], ['check', '--api', 'chat', '--api', 'responses']]) {
  test(`invalid arguments cannot read configuration: ${args.join(' ')}`, async () => {
    const f = fixture()
    await assert.rejects(runOpenAI(args, f.options), { message: 'HF_OPENAI_ARGUMENTS' })
    assert.deepEqual(f.reads, [])
    assert.deepEqual(f.calls, [])
  })
}

test('check errors preserve safe HTTP status without retries, body or exception details', async () => {
  for (const outcome of [() => new Response('fixture-secret', { status: 429 }), () => { throw new Error('fixture-secret') }, () => Response.json({ error: 'fixture-secret' })]) {
    let count = 0
    const f = fixture({ fetch: async () => { count++; return outcome() } })
    await assert.rejects(runOpenAI(['check', '--api', 'chat'], f.options), /^(Error: )?HF_OPENAI_(HTTP_429|CHECK_FAILED|RESPONSE)$/)
    assert.equal(count, 1)
    assert.doesNotMatch(f.logs.join(' '), /fixture-secret/)
  }
})

test('malformed successful response cannot leak an upstream value or count as a check pass', async () => {
  const f = fixture({ fetch: async () => Response.json({ output: { private: 'fixture-secret' } }) })
  await assert.rejects(runOpenAI(['check', '--api', 'responses'], f.options), { message: 'HF_OPENAI_RESPONSE' })
})

for (const [api, webPort, apiPort] of [['chat', 45173, 48080], ['responses', 55173, 58080]]) {
  test(`${api} smoke owns independent Compose resources and sends secrets only through Compose env`, async () => {
    const f = fixture()
    await runOpenAI(['smoke', '--api', api], f.options)
    assert.equal(f.reads[0], `${root}/.env`)
    const compose = f.calls.filter(({ args }) => args?.[0] === 'compose')
    assert.equal(compose.length, 2)
    const project = compose[0].args[4]
    assert.match(project, new RegExp(`^harness-forge-openai-smoke-${api}-[0-9a-f]{24}$`))
    assert.deepEqual(f.calls.slice(0, 3).map(({ args }) => args), ['container', 'volume', 'network'].map((kind) => [kind, 'ls', kind === 'container' ? '-aq' : '-q', '--filter', `label=com.docker.compose.project=${project}`]))
    assert.deepEqual(compose[0].args.slice(0, 9), ['compose', '--env-file', '/dev/null', '-p', project, '-f', `${root}/docker-compose.yaml`, '-f', `${root}/docker-compose.openai.yaml`])
    assert(compose[0].args.includes('up'))
    assert.equal(compose[1].args[4], project)
    assert.deepEqual(compose[1].args.slice(-4), ['down', '-v', '--remove-orphans', '--timeout=10'])
    assert(f.logs.some((line) => line === `HF_OPENAI_SMOKE_START mode=${api} project=${project}`))
    const env = compose[0].options.env
    assert.equal(env.HF_MODEL_BACKEND, `openai-${api}`)
    assert.equal(env.HF_GATEWAY_URL, 'http://model-gateway:4000')
    assert.match(env.HF_GATEWAY_KEY, /^sk-hf-[0-9a-f]{64}$/)
    assert.equal(env.HF_GATEWAY_MODE, api)
    assert.equal(env.HF_OPENAI_MODEL, `${api}-fixture`)
    assert.equal(env.HF_GATEWAY_UPSTREAM_MODEL, `openai/${api}-fixture`)
    assert.equal(env.OPENAI_API_KEY, 'fixture-secret')
    assert.equal(env.OPENAI_BASE_URL, 'https://api.example/v1')
    assert.equal(env.HF_OPENAI_BASE_URL, env.OPENAI_BASE_URL)
    assert.equal(env.ANTHROPIC_API_KEY, '')
    assert.equal(env.SANDBOX_PROVIDER, 'docker')
    assert.match(env.DATABASE_URL, /@postgres:5432/)
    assert.equal(env.MINIO_ENDPOINT, 'http://minio:9000')
    assert.equal(env.WEB_PORT, `${webPort}`)
    assert.equal(env.CONTROL_PLANE_PORT, `${apiPort}`)
    assert.equal(env.DOCKER_CONTEXT, 'fixture-context')
    for (const name of ['COMPOSE_FILE', 'COMPOSE_ENV_FILES', 'COMPOSE_PROJECT_NAME', 'UNRELATED_SECRET', 'NODE_OPTIONS']) assert.equal(env[name], undefined)
    assert.equal(f.smoke.length, 1)
    assert.equal(f.smoke[0].fullUI, true)
    assert.equal(f.smoke[0].webURL, `http://localhost:${webPort}`)
    assert.equal(f.smoke[0].baseURL, `http://localhost:${apiPort}`)
    const install = f.calls.find(({ command }) => command === 'pnpm')
    assert.deepEqual(install.args, ['exec', 'playwright', 'install', 'chromium'])
    assert.equal(install.options.cwd, `${root}/apps/web`)
    assert.equal(install.options.env.OPENAI_API_KEY, undefined)
    assert.equal(install.options.env.HF_GATEWAY_KEY, undefined)
    assert.doesNotMatch(JSON.stringify(f.calls.map(({ args }) => args)), /fixture-secret|foreign|fixture-key/)
    assert.doesNotMatch(f.logs.join(' '), /fixture-secret|foreign|fixture-answer/)
  })
}

for (const kind of ['container', 'volume', 'network']) {
  test(`smoke refuses preexisting ${kind} without up, smoke or cleanup`, async () => {
    const f = fixture()
    f.options.run = async (command, args) => { f.calls.push({ command, args }); return args[0] === kind ? 'existing-id\n' : '' }
    await assert.rejects(runOpenAI(['smoke', '--api', 'chat'], f.options), { message: 'HF_OPENAI_RESOURCES_EXIST' })
    assert(!f.calls.some(({ args }) => args.includes('compose')))
    assert.deepEqual(f.smoke, [])
  })
}

test('concurrent same-protocol smoke invocations own distinct projects and cannot clean each other', async () => {
  const resources = new Set(), projects = [], cleanups = [[], []]
  let started = 0, startBoth, firstCleaned, secondRetained
  const bothStarted = new Promise((resolve) => { startBoth = resolve })
  const firstCleanup = new Promise((resolve) => { firstCleaned = resolve })
  const runs = [0, 1].map((index) => {
    const f = fixture({
      run: async (_command, args) => {
        if (args.includes('up')) {
          projects[index] = args[args.indexOf('-p') + 1]
          resources.add(projects[index])
          if (++started === 2) startBoth()
          await bothStarted
        } else if (args.includes('down')) {
          const project = args[args.indexOf('-p') + 1]
          cleanups[index].push(project)
          resources.delete(project)
          if (index === 0) firstCleaned()
        }
        return ''
      },
      smokeRunner: async () => {
        if (index === 1) { await firstCleanup; secondRetained = resources.has(projects[1]) }
        return evidence
      },
    })
    return runOpenAI(['smoke', '--api', 'chat'], f.options)
  })
  await Promise.all(runs)
  assert.notEqual(projects[0], projects[1])
  assert.equal(secondRetained, true, 'first cleanup removed the second invocation resources')
  assert.deepEqual(cleanups, projects.map((project) => [project]))
  assert.equal(resources.size, 0)
})

for (const phase of ['up', 'smoke', 'down', 'abort']) {
  test(`smoke ${phase} failure cleans only the owned project and reports a safe code`, async () => {
    const controller = new AbortController()
    const f = fixture({ signal: controller.signal })
    f.options.run = async (command, args, options) => {
      f.calls.push({ command, args, options })
      if (args.includes(phase)) throw new Error('fixture-secret')
      if (phase === 'abort' && args.includes('up')) controller.abort()
      return ''
    }
    if (phase === 'smoke') f.options.smokeRunner = async () => { throw new Error('fixture-secret') }
    await assert.rejects(runOpenAI(['smoke', '--api', 'chat'], f.options), /HF_OPENAI_(START_FAILED|SMOKE_FAILED|CLEANUP_FAILED|ABORTED)/)
    const cleanup = f.calls.filter(({ args }) => args.includes('down'))
    assert.equal(cleanup.length, 1)
    assert.match(cleanup[0].args[4], /^harness-forge-openai-smoke-chat-[0-9a-f]{24}$/)
    assert.equal(cleanup[0].args[4], f.calls.find(({ args }) => args.includes('up')).args[4])
    assert.equal(cleanup[0].options.signal, undefined)
    assert.doesNotMatch(f.logs.join(' '), /fixture-secret/)
  })
}

test('dev uses a dedicated persistent project and never automatically deletes volumes', async () => {
  const f = fixture()
  await runOpenAI(['dev', '--api', 'responses'], f.options)
  assert.equal(f.calls.length, 1)
  assert(f.calls[0].args.includes('harness-forge-openai'))
  assert(f.calls[0].args.includes('up'))
  assert.equal(f.calls[0].options.env.WEB_PORT, '5173')
  assert.deepEqual(f.smoke, [])
})

test('default smoke subprocess imports existing runner but cannot access upstream or gateway credentials', async () => {
  const f = fixture({ smokeRunner: undefined })
  await runOpenAI(['smoke', '--api', 'chat'], f.options)
  const child = f.calls.find(({ command }) => command === process.execPath)
  assert.match(child.args[2], /import \{ runSmoke \} from '\.\/apps\/web\/scripts\/smoke-claude.mjs'/)
  assert.match(child.args[2], /fullUI:true/)
  assert.equal(child.options.env.SMOKE_API_URL, 'http://localhost:48080')
  assert.equal(child.options.env.SMOKE_WEB_URL, 'http://localhost:45173')
  assert.equal(child.options.env.OPENAI_API_KEY, undefined)
  assert.equal(child.options.env.HF_GATEWAY_KEY, undefined)
})

for (const result of [undefined, {}, { ...evidence, run_ids: [id(4)] }, { ...evidence, conversation_ids: [id(2), id(2)] }, { ...evidence, artifact_ids: ['fixture-secret', id(8)] }]) {
  test(`incomplete full-UI evidence fails instead of accepting the old one-run smoke: ${JSON.stringify(result)}`, async () => {
    const f = fixture({ smokeRunner: async () => result })
    await assert.rejects(runOpenAI(['smoke', '--api', 'chat'], f.options), { message: 'HF_OPENAI_SMOKE_FAILED' })
    assert(f.calls.some(({ args }) => args.includes('down')))
    assert(!f.logs.some((line) => line.includes('SMOKE_OK')))
    assert.doesNotMatch(f.logs.join(' '), /fixture-secret/)
  })
}

test('successful smoke returns and logs only the whitelisted full-UI UUID evidence', async () => {
  const f = fixture({ smokeRunner: async () => ({ ...evidence, private_payload: 'fixture-secret' }) })
  assert.deepEqual(await runOpenAI(['smoke', '--api', 'chat'], f.options), evidence)
  assert.match(f.logs.join(' '), new RegExp(evidence.run_ids[2]))
  assert.doesNotMatch(f.logs.join(' '), /private_payload|fixture-secret/)
})

test('real launcher SIGTERM waits for startup subprocess to close, then cleans owned resources', async () => {
  const temporary = await mkdtemp(join(tmpdir(), 'hf-openai-cli-'))
  let child
  try {
    await writeFile(join(temporary, 'test.env'), dotenv)
    await writeFile(join(temporary, 'docker'), `#!${process.execPath}
const {appendFileSync} = require('node:fs');
const trace = process.env.HOME + '/trace';
const args = process.argv.slice(2);
console.error('fixture-secret');
if (args.includes('up')) {
  appendFileSync(trace, 'up\\n');
  process.on('SIGTERM', () => setTimeout(() => { appendFileSync(trace, 'up-closed\\n'); process.exit(3); }, 100));
  setInterval(() => {}, 1000);
} else if (args.includes('down')) appendFileSync(trace, 'down\\n');
`, { mode: 0o755 })
    child = spawn(process.execPath, [`${root}/scripts/openai.mjs`, 'smoke', '--api', 'chat', '--env-file', join(temporary, 'test.env')], {
      cwd: temporary, env: { PATH: temporary, HOME: temporary }, stdio: ['ignore', 'pipe', 'pipe'],
    })
    let output = ''
    child.stdout.on('data', (chunk) => { output += chunk })
    child.stderr.on('data', (chunk) => { output += chunk })
    const completed = new Promise((resolve) => child.once('close', (code) => resolve(code)))
    const deadline = Date.now() + 5000
    while (!(await readFile(join(temporary, 'trace'), 'utf8').catch(() => '')).includes('up\n')) {
      assert(Date.now() < deadline, 'fixture startup did not occur')
      await sleep(20)
    }
    child.kill('SIGTERM')
    assert.equal(await completed, 143)
    assert.equal(await readFile(join(temporary, 'trace'), 'utf8'), 'up\nup-closed\ndown\n')
    assert.match(output, /HF_OPENAI_ABORTED/)
    assert.doesNotMatch(output, /fixture-secret/)
  } finally { child?.kill('SIGKILL'); await rm(temporary, { recursive: true, force: true }) }
})

test('overlay isolates gateway credentials and adds neither host ports nor native-mode changes', async () => {
  const overlay = await readFile(new URL('../docker-compose.openai.yaml', import.meta.url), 'utf8')
  assert.match(overlay, /model-gateway:/)
  assert.doesNotMatch(overlay, /ports:|env_file:/)
  assert.equal((overlay.match(/^\s+OPENAI_API_KEY:/gm) ?? []).length, 1)
  assert.match(overlay, /ANTHROPIC_API_KEY: ""/)
  assert.match(overlay, /SANDBOX_PROVIDER: docker/)
  assert.match(overlay, /condition: service_healthy/)
  const ignored = await readFile(new URL('../.dockerignore', import.meta.url), 'utf8')
  for (const pattern of ['.env', '.git', '.worktrees', '**/node_modules', '**/.venv']) assert(ignored.split('\n').includes(pattern))
})
