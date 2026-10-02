import { spawn } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import { readFile as readFileDefault } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { parseEnv } from 'node:util'

const root = fileURLToPath(new URL('../', import.meta.url)).replace(/\/$/, '')
const fail = (code) => new Error(`HF_OPENAI_${code}`)

// Capture, never relay raw child logs: model errors and Compose output can contain secrets.
function runCommand(command, args, { env, cwd = root, signal } = {}) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(fail('ABORTED')); return }
    const child = spawn(command, args, { env, cwd, detached: true, stdio: ['ignore', 'pipe', 'ignore'] })
    let output = '', overflow = false, failed = false, timer
    const kill = (name) => { try { process.kill(-child.pid, name) } catch { /* Already exited. */ } }
    const abort = () => { kill('SIGTERM'); timer ??= setTimeout(() => kill('SIGKILL'), 5000) }
    signal?.addEventListener('abort', abort, { once: true })
    child.stdout.on('data', (chunk) => {
      output += chunk.toString()
      if (output.length > 1024 * 1024) { overflow = true; output = ''; abort() }
    })
    // Even on an error, wait for close before Compose cleanup can begin.
    child.on('error', () => { failed = true })
    child.on('close', (code) => {
      clearTimeout(timer)
      signal?.removeEventListener('abort', abort)
      if (code !== 0 || failed || overflow || signal?.aborted) reject(fail('COMMAND_FAILED'))
      else resolve(output)
    })
  })
}

function platformEnvironment(env) {
  return Object.fromEntries(Object.entries(env).filter(([name]) =>
    /^(PATH|HOME|USER|LOGNAME|SHELL|TMPDIR|TMP|TEMP|XDG_RUNTIME_DIR|XDG_CONFIG_HOME|SSH_AUTH_SOCK|LANG|SSL_CERT_FILE|SSL_CERT_DIR|NODE_EXTRA_CA_CERTS|HTTPS?_PROXY|ALL_PROXY|NO_PROXY|https?_proxy|all_proxy|no_proxy)$/.test(name)
    || /^(DOCKER_|LC_)/.test(name)))
}

function argumentsFor(argv) {
  const [command, ...flags] = argv
  if (!['check', 'dev', 'smoke'].includes(command) || flags.length % 2) throw fail('ARGUMENTS')
  const values = new Map()
  for (let i = 0; i < flags.length; i += 2) {
    const flag = flags[i], value = flags[i + 1]
    if (!['--api', '--env-file'].includes(flag) || values.has(flag) || !value || value.startsWith('--')) throw fail('ARGUMENTS')
    values.set(flag, value)
  }
  const api = values.get('--api')
  if (!['chat', 'responses'].includes(api)) throw fail('ARGUMENTS')
  return { command, api, envFile: values.get('--env-file') ?? `${root}/.env` }
}

async function configuration(envFile, api, readFile) {
  let fields
  try { fields = parseEnv(await readFile(envFile, 'utf8')) } catch { throw fail('ENV_FILE') }
  const key = fields.OPENAI_API_KEY, model = fields[api === 'chat' ? 'OPENAI_CHAT_MODEL' : 'OPENAI_RESPONSES_MODEL']
  const value = fields.OPENAI_BASE_URL
  try {
    if (!key?.trim() || !model?.trim() || /[\x00-\x1f\x7f]/.test(key) || /\s|[\x00-\x1f\x7f]/.test(model)
      || !value || /\s|[\\\x00-\x1f\x7f?#]/.test(value) || /:\/\/[^/]*@/.test(value)) throw fail('CONFIG')
    const url = new URL(value)
    if (url.protocol !== 'https:' || !url.hostname || url.username || url.password) throw fail('CONFIG')
    url.pathname = url.pathname.replace(/\/+$/, '') || '/v1'
    return { key, model, base: url.href.replace(/\/$/, ''), modelHash: createHash('sha256').update(model).digest('hex').slice(0, 12) }
  } catch { throw fail('CONFIG') }
}

function deployment(api, command, config, platform) {
  const smoke = command === 'smoke'
  const ports = smoke ? (api === 'chat' ? [45173, 48080, 48081, 48090, 45432, 49000, 49001] : [55173, 58080, 58081, 58090, 55432, 59000, 59001]) : [5173, 8080, 8081, 8090, 5432, 9000, 9001]
  const project = smoke ? `harness-forge-openai-smoke-${api}-${randomBytes(12).toString('hex')}` : 'harness-forge-openai'
  const env = {
    ...platform,
    ...Object.fromEntries(['WEB_PORT', 'CONTROL_PLANE_PORT', 'ARTIFACT_PORT', 'RUNTIME_PORT', 'POSTGRES_PORT', 'MINIO_PORT', 'MINIO_CONSOLE_PORT'].map((name, index) => [name, String(ports[index])])),
    WEB_ORIGIN: `http://localhost:${ports[0]}`, ARTIFACT_PUBLIC_ORIGIN: `http://localhost:${ports[2]}`,
    POSTGRES_DB: 'harness_forge', POSTGRES_USER: 'harness_forge', POSTGRES_PASSWORD: 'local-dev-only',
    DATABASE_URL: 'postgres://harness_forge:local-dev-only@postgres:5432/harness_forge?sslmode=disable',
    MINIO_ENDPOINT: 'http://minio:9000', MINIO_ROOT_USER: 'harness_forge', MINIO_ROOT_PASSWORD: 'local-dev-only',
    MINIO_ACCESS_KEY: 'harness_forge', MINIO_SECRET_KEY: 'local-dev-only', MINIO_BUCKET: 'harness-forge',
    RUNTIME_URL: 'http://agent-runtime:8090', WORKSPACE_ROOT: '/workspaces', RUN_WORKSPACE_ROOT: '/workspaces',
    RUNTIME_STATE_ROOT: '/sessions/executions', CLAUDE_CONFIG_DIR: '/sessions/claude',
    SANDBOX_PROVIDER: 'docker', ANTHROPIC_API_KEY: '', ANTHROPIC_BASE_URL: '',
    HF_MODEL_BACKEND: `openai-${api}`, HF_GATEWAY_URL: 'http://model-gateway:4000', HF_GATEWAY_KEY: `sk-hf-${randomBytes(32).toString('hex')}`,
    HF_OPENAI_BASE_URL: config.base, HF_OPENAI_MODEL: config.model,
    HF_GATEWAY_MODE: api, HF_GATEWAY_UPSTREAM_MODEL: `openai/${config.model}`, OPENAI_BASE_URL: config.base, OPENAI_API_KEY: config.key,
  }
  const compose = ['compose', '--env-file', '/dev/null', '-p', project, '-f', `${root}/docker-compose.yaml`, '-f', `${root}/docker-compose.openai.yaml`]
  return { project, env, compose, webURL: env.WEB_ORIGIN, baseURL: `http://localhost:${ports[1]}` }
}

function smokeEvidence(result) {
  const uuid = (value) => typeof value === 'string' && /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/.test(value)
  if (!uuid(result?.project_id)) throw fail('SMOKE_FAILED')
  const safe = { project_id: result.project_id }
  for (const [field, minimum] of [['conversation_ids', 2], ['run_ids', 3], ['artifact_ids', 2]]) {
    const ids = result[field]
    if (!Array.isArray(ids) || ids.length < minimum || !ids.every(uuid) || new Set(ids).size !== ids.length) throw fail('SMOKE_FAILED')
    safe[field] = ids
  }
  return safe
}

// Only boundary I/O is injectable; ordinary tests never read credentials or call paid APIs.
export async function runOpenAI(argv, {
  readFile = readFileDefault, fetch = globalThis.fetch, run = runCommand,
  env = process.env, signal, smokeRunner, log = console.log,
} = {}) {
  const { command, api, envFile } = argumentsFor(argv)
  const config = await configuration(envFile, api, readFile)
  const checkAbort = () => { if (signal?.aborted) throw fail('ABORTED') }
  const perform = async (code, action) => {
    checkAbort()
    try { const result = await action(); checkAbort(); return result }
    catch { throw fail(signal?.aborted ? 'ABORTED' : code) }
  }
  checkAbort()
  if (command === 'check') {
    const body = { model: config.model, stream: false, store: false,
      ...(api === 'chat' ? { max_completion_tokens: 64, messages: [{ role: 'user', content: 'Reply with OK.' }] } : { max_output_tokens: 64, input: 'Reply with OK.' }),
    }
    const response = await perform('CHECK_FAILED', () => fetch(`${config.base}/${api === 'chat' ? 'chat/completions' : 'responses'}`, {
      method: 'POST', headers: { Authorization: `Bearer ${config.key}`, 'Content-Type': 'application/json' },
      body: JSON.stringify(body), redirect: 'error', signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30000)]) : AbortSignal.timeout(30000),
    }))
    if (!response.ok) {
      try { await response.body?.cancel() } catch { /* Preserve the HTTP status, never the upstream body. */ }
      throw fail(`HTTP_${response.status}`)
    }
    const result = await perform('RESPONSE', () => response.json())
    const text = await perform('RESPONSE', async () => api === 'chat' ? result.choices?.[0]?.message?.content : result.output?.flatMap((item) => item.content ?? []).find((item) => item.type === 'output_text')?.text)
    if (typeof text !== 'string' || !text.trim()) throw fail('RESPONSE')
    log(`HF_OPENAI_CHECK_OK mode=${api} status=${response.status} model_sha256=${config.modelHash}`)
    return
  }

  const platform = platformEnvironment(env)
  const { project, env: childEnv, compose, webURL, baseURL } = deployment(api, command, config, platform)
  const up = () => run('docker', [...compose, 'up', '-d', '--build', '--wait', '--wait-timeout', '180'], { env: childEnv, cwd: root, signal })
  if (command === 'dev') {
    await perform('START_FAILED', up)
    log(`HF_OPENAI_DEV_READY mode=${api} model_sha256=${config.modelHash} web=${webURL} project=${project}`)
    return
  }

  for (const kind of ['container', 'volume', 'network']) {
    const existing = await perform('RESOURCE_CHECK_FAILED', () => run('docker', [kind, 'ls', kind === 'container' ? '-aq' : '-q', '--filter', `label=com.docker.compose.project=${project}`], { env: platform, cwd: root, signal }))
    if (existing.trim()) throw fail('RESOURCES_EXIST')
  }
  log(`HF_OPENAI_SMOKE_START mode=${api} project=${project}`)
  let failure, evidence
  try {
    await perform('START_FAILED', up)
    await perform('BROWSER_INSTALL_FAILED', () => run('pnpm', ['exec', 'playwright', 'install', 'chromium'], { env: platform, cwd: `${root}/apps/web`, signal }))
    // A separate process lets cancellation stop the browser/model workflow before volume cleanup.
    smokeRunner ??= async ({ fullUI, webURL, baseURL }) => JSON.parse(await run(process.execPath, ['--input-type=module', '--eval',
      `import { runSmoke } from './apps/web/scripts/smoke-claude.mjs'; const r=await runSmoke({fullUI:${fullUI},webURL:process.env.SMOKE_WEB_URL,baseURL:process.env.SMOKE_API_URL,log:()=>{}}); console.log(JSON.stringify({project_id:r?.project_id,conversation_ids:r?.conversation_ids,run_ids:r?.run_ids,artifact_ids:r?.artifact_ids}));`,
    ], { env: { ...platform, SMOKE_WEB_URL: webURL, SMOKE_API_URL: baseURL }, cwd: root, signal }))
    evidence = smokeEvidence(await perform('SMOKE_FAILED', () => smokeRunner({ fullUI: true, webURL, baseURL, signal, log: () => {} })))
  } catch (error) { failure = error }
  finally {
    // Do not pass an aborted signal: cleanup must finish after startup/smoke failures and Ctrl-C.
    try { await run('docker', [...compose, 'down', '-v', '--remove-orphans', '--timeout=10'], { env: childEnv, cwd: root }) }
    catch { log('HF_OPENAI_CLEANUP_FAILED'); failure ??= fail('CLEANUP_FAILED') }
  }
  if (failure) throw failure
  log(`HF_OPENAI_SMOKE_OK mode=${api} model_sha256=${config.modelHash} cleanup=ok evidence=${JSON.stringify(evidence)}`)
  return evidence
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const controller = new AbortController()
  const interrupt = () => { process.exitCode = 130; controller.abort() }
  const terminate = () => { process.exitCode = 143; controller.abort() }
  process.on('SIGINT', interrupt)
  process.on('SIGTERM', terminate)
  try { await runOpenAI(process.argv.slice(2), { signal: controller.signal }) }
  catch (error) { console.error(/^HF_OPENAI_[A-Z_0-9]+$/.test(error.message) ? error.message : 'HF_OPENAI_FAILED'); process.exitCode ||= 1 }
  finally { process.off('SIGINT', interrupt); process.off('SIGTERM', terminate) }
}
