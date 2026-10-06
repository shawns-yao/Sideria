// Isolated E2E harness. Requires explicit test-only database/Redis URLs.
import { spawn, execFileSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
const root = resolve(import.meta.dirname, '..')
const database = process.env.SIDERIA_TEST_DATABASE
const redis = process.env.SIDERIA_TEST_REDIS
if (!database || !redis || !['127.0.0.1', 'localhost'].includes(new URL(database).hostname) || !['127.0.0.1', 'localhost'].includes(new URL(redis).hostname)) throw new Error('Explicit loopback test database and Redis required')
const temp = await mkdtemp(join(tmpdir(), 'sideria-e2e-'))
const token = randomBytes(32).toString('hex')
const base = 'http://127.0.0.1:18080'
const children = [], logs = []
let dockerID = ''
function start(binary, env) { const child = spawn(join(root, 'bin', binary), [], { cwd: root, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] }); child.stdout.on('data', b => logs.push(b.toString())); child.stderr.on('data', b => logs.push(b.toString())); children.push(child); return child }
const serverEnv = { DATABASE_URL: database, REDIS_URL: redis, SIDERIA_ADMIN_TOKEN: token, SIDERIA_DEV: '1', SIDERIA_LISTEN: '127.0.0.1:18080', SIDERIA_TRANSFER_DIR: join(temp, 'transfers'), SIDERIA_AI_ALLOW_EGRESS: '0', SIDERIA_AI_KEY: '' }
const server = start('sideria-server', serverEnv)
let cookie = ''
async function api(path, body) { const response = await fetch(base + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie }, body: body === undefined ? undefined : JSON.stringify(body) }); if (response.headers.get('set-cookie')) cookie = response.headers.get('set-cookie').split(';')[0]; const data = await response.json(); if (!response.ok) throw new Error(path + ': ' + JSON.stringify(data)); return data }
const deadline = Date.now() + 20000
while (true) { try { await api('/healthz'); break } catch { if (Date.now() > deadline) throw new Error('Server not ready: ' + logs.join('\n')); await new Promise(r => setTimeout(r, 100)) } }
await api('/api/login', { token })
for (const previous of await api('/api/hosts')) { if (previous.name.startsWith('E2E ')) await api(`/api/hosts/${previous.id}/revoke`, {}) }
const nodes = []
for (const [index, name] of ['E2E Alpha', 'E2E Beta'].entries()) {
  const directory = join(temp, `root-${index}`); await mkdir(directory); await writeFile(join(directory, 'hello.txt'), `${name} file evidence\n`)
  execFileSync('git', ['init', '-q', '-b', index ? 'release' : 'main', directory])
  execFileSync('git', ['-C', directory, 'add', 'hello.txt'])
  execFileSync('git', ['-C', directory, '-c', 'user.name=E2E', '-c', 'user.email=e2e@example.invalid', 'commit', '-qm', 'fixture'])
  if (!index) await writeFile(join(directory, 'dirty.txt'), 'local change\n')
  const registration = await api('/api/hosts', { name })
  const agent = start('sideria-agent', { SIDERIA_SERVER_URL: base, SIDERIA_ENROLLMENT_TOKEN: registration.enrollment_token, SIDERIA_AGENT_STATE: join(temp, `state-${index}`), SIDERIA_FILE_ROOT: directory, SIDERIA_DEV: '1', SIDERIA_ALLOW_TERMINAL: '1', SIDERIA_SAMPLE_INTERVAL: '1s', SIDERIA_DOCKER_SOCKET: process.env.SIDERIA_TEST_DOCKER_SOCKET ?? '' })
  nodes.push({ id: registration.id, name, root: directory, pid: agent.pid })
}
if (process.env.SIDERIA_TEST_DOCKER_SOCKET) dockerID = execFileSync('docker', ['run', '-d', '--name', `sideria-e2e-${randomBytes(4).toString('hex')}`, 'alpine:3.22', 'sh', '-c', 'echo E2E_DOCKER_LOG; sleep 3600'], { encoding: 'utf8' }).trim()
await mkdir(join(root, '.run'), { recursive: true })
await writeFile(join(root, '.run/e2e.json'), JSON.stringify({ base, token, nodes, dockerID, serverPID: server.pid, serverEnv }), { mode: 0o600 })
console.log('Isolated Sideria E2E fixture ready on ' + base)
let stopping = false
async function stop() { if (stopping) return; stopping = true; for (const child of children) child.kill('SIGTERM'); await Promise.all(children.map(c => c.exitCode !== null ? null : new Promise(resolve => { c.once('exit', resolve); setTimeout(() => { c.kill('SIGKILL'); resolve() }, 5000).unref() }))); if (dockerID) { try { execFileSync('docker', ['rm', '-f', dockerID], { stdio: 'ignore' }) } catch {} } await rm(temp, { recursive: true, force: true }); await rm(join(root, '.run/e2e.json'), { force: true }); process.exit(0) }
process.on('SIGTERM', stop); process.on('SIGINT', stop)
