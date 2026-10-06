// Fault injection against scripts/fixture.mjs only. Never takes arbitrary hosts or credentials.
import { readFile, writeFile } from 'node:fs/promises'
import { spawn, execFileSync } from 'node:child_process'
import { resolve, dirname, join } from 'node:path'
const root = resolve(import.meta.dirname, '..')
const fixture = JSON.parse(await readFile(join(root, '.run/e2e.json'), 'utf8'))
if (fixture.base !== 'http://127.0.0.1:18080') throw new Error('Not an isolated fixture')
const children = [], evidence = []
let cookie = ''
async function api(path, body, key) { const response = await fetch(fixture.base + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie, ...(key ? { 'Idempotency-Key': key } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) }); if (response.headers.get('set-cookie')) cookie = response.headers.get('set-cookie').split(';')[0]; const value = await response.json(); return { status: response.status, value } }
async function until(name, check, timeout = 20000) { const deadline = Date.now() + timeout; while (Date.now() < deadline) { try { if (await check()) { evidence.push({ scenario: name, passed: true }); return } } catch {} await new Promise(r => setTimeout(r, 100)) } throw new Error('Timeout: ' + name) }
function start(binary, env) { const child = spawn(join(root, 'bin', binary), [], { cwd: root, env: { ...process.env, ...env }, stdio: 'ignore' }); children.push(child); return child }
function assert(value, message) { if (!value) throw new Error(message) }
try {
  await api('/api/login', { token: fixture.token })
  const n = fixture.nodes[0]
  process.kill(n.pid, 'SIGTERM')
  await until('agent disconnect is visible', async () => !(await api('/api/hosts')).value.find(h => h.id === n.id).online)
  const accepted = await api(`/api/hosts/${n.id}/tasks`, { action: 'file.download', params: { path: 'hello.txt' }, confirm: true }, 'process-recovery-download')
  assert(accepted.status === 202 && accepted.value.state === 'pending', 'task not durably pending')
  process.kill(fixture.serverPID, 'SIGKILL')
  await new Promise(r => setTimeout(r, 200))
  const center = start('sideria-server', fixture.serverEnv)
  await until('center restart recovers persistent session and pending task', async () => { const task = await api(`/api/tasks/${accepted.value.id}`); return task.status === 200 && task.value.attempt_id === accepted.value.attempt_id && task.value.state === 'pending' })
  const agentEnv = { SIDERIA_SERVER_URL: fixture.base, SIDERIA_AGENT_STATE: join(dirname(n.root), 'state-0'), SIDERIA_FILE_ROOT: n.root, SIDERIA_DEV: '1', SIDERIA_ALLOW_TERMINAL: '1', SIDERIA_SAMPLE_INTERVAL: '1s', SIDERIA_DOCKER_SOCKET: process.env.SIDERIA_TEST_DOCKER_SOCKET ?? '' }
  let node = start('sideria-agent', agentEnv)
  await until('pending dispatch completes after center and agent restart with same attempt', async () => { const task = (await api(`/api/tasks/${accepted.value.id}`)).value; return task.state === 'succeeded' && task.attempt_id === accepted.value.attempt_id })
  if (fixture.dockerID) {
    const accepted = await api(`/api/hosts/${n.id}/tasks`, { action: 'docker.restart', params: { container: fixture.dockerID }, confirm: true }, 'crash-during-docker')
    assert(accepted.status === 202, 'restart task rejected')
    await until('Agent persists running before side effect completes', async () => (await api(`/api/tasks/${accepted.value.id}`)).value.state === 'running')
    process.kill(node.pid, 'SIGKILL')
    await new Promise(r => setTimeout(r, 200))
    node = start('sideria-agent', agentEnv)
    await until('Agent crash becomes uncertain without a new attempt', async () => { const task = (await api(`/api/tasks/${accepted.value.id}`)).value; return task.state === 'uncertain' && task.attempt_id === accepted.value.attempt_id })
    const blocked = await api(`/api/hosts/${n.id}/tasks`, { action: 'docker.restart', params: { container: fixture.dockerID }, confirm: true }, 'blocked-after-uncertainty')
    await until('uncertain resource rejects a new conflicting write even after lease expiry', async () => { const task = (await api(`/api/tasks/${blocked.value.id}`)).value; return task.state === 'failed' && task.error.includes('resource conflict') }, 45000)
  }
  const redisContainer = process.env.SIDERIA_TEST_REDIS_CONTAINER
  if (redisContainer) {
    execFileSync('docker', ['stop', redisContainer], { stdio: 'ignore' })
    try { const denied = await api('/api/hosts', { name: 'must not register during outage' }); assert(denied.status === 503, 'Redis outage failed open'); evidence.push({ scenario: 'Redis outage refuses new registration', passed: true }) } finally { execFileSync('docker', ['start', redisContainer], { stdio: 'ignore' }) }
    await until('Redis restart restores readiness without losing task facts', async () => (await api('/healthz')).status === 200 && (await api(`/api/tasks/${accepted.value.id}`)).value.state === 'succeeded')
  }
  const diag = (await api('/api/diagnostics')).value
  evidence.push({ scenario: 'post-recovery diagnostics', data: diag })
  await writeFile(join(root, '.run/recovery-results.json'), JSON.stringify(evidence, null, 2))
  console.log(JSON.stringify(evidence, null, 2))
  // Keep the surviving replacements discoverable for the next isolated test step.
  fixture.serverPID = center.pid; fixture.nodes[0].pid = node.pid
} finally {
  for (const child of children) if (child.exitCode === null) child.kill('SIGTERM')
}
