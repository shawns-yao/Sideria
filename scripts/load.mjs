// Bounded local workload; accepts only the ephemeral fixture's loopback endpoint.
import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { join, resolve } from "node:path";
import { performance } from "node:perf_hooks";

const root = resolve(import.meta.dirname, "..");
const fixture = JSON.parse(await readFile(join(root, ".run/e2e.json"), "utf8"));
assert.equal(fixture.base, "http://127.0.0.1:18080");
assert(fixture.nodes.length >= 2 && fixture.nodes.length <= 8);
const nodes = fixture.nodes,
  run = randomUUID(),
  children = [];
let cookie = "",
  backpressure = 0,
  sampling = false;
const memory = { center_rss_peak_bytes: 0, agents_rss_peak_bytes: 0 };
const report = {
  kind: "isolated-local-processes-not-real-servers",
  agents: nodes.length,
  samples_per_second: nodes.length,
  bytes_per_file: 256 * 1024,
  concurrency: 8,
  phases: {},
};
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const hash = (b) => createHash("sha256").update(b).digest("hex");
const stats = (values) => {
  const a = [...values].sort((a, b) => a - b);
  return {
    count: a.length,
    p50_ms: a[Math.floor((a.length - 1) * 0.5)] ?? 0,
    p95_ms: a[Math.floor((a.length - 1) * 0.95)] ?? 0,
    max_ms: a.at(-1) ?? 0,
  };
};
async function api(path, body, headers = {}, retry = false) {
  for (let tries = 0; ; tries++) {
    const response = await fetch(fixture.base + path, {
      method: body === undefined ? "GET" : "POST",
      headers: {
        Cookie: cookie,
        "Content-Type": "application/json",
        ...headers,
      },
      body:
        body === undefined
          ? undefined
          : Buffer.isBuffer(body)
            ? body
            : JSON.stringify(body),
      signal: AbortSignal.timeout(30000),
    });
    if (response.headers.get("set-cookie"))
      cookie = response.headers.get("set-cookie").split(";")[0];
    const value = await response.json();
    if (retry && response.status === 429 && tries < 30) {
      backpressure++;
      await delay(100 + tries * 25);
      continue;
    }
    assert(
      response.ok,
      `${path}: HTTP ${response.status} ${JSON.stringify(value)}`,
    );
    return value;
  }
}
async function until(check, budget = 60000) {
  const deadline = performance.now() + budget;
  while (performance.now() < deadline) {
    if (await check()) return;
    await delay(100);
  }
  throw new Error("bounded workload deadline exceeded");
}
async function parallel(items, work, limit = 8) {
  let cursor = 0;
  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, async () => {
      while (cursor < items.length) {
        const i = cursor++;
        await work(items[i], i);
      }
    }),
  );
}
async function rss(pid) {
  try {
    const text = await readFile(`/proc/${pid}/status`, "utf8");
    return Number(text.match(/^VmRSS:\s+(\d+)/m)?.[1] ?? 0) * 1024;
  } catch {
    return 0;
  }
}
async function sample() {
  if (sampling) return;
  sampling = true;
  try {
    memory.center_rss_peak_bytes = Math.max(
      memory.center_rss_peak_bytes,
      await rss(fixture.serverPID),
    );
    memory.agents_rss_peak_bytes = Math.max(
      memory.agents_rss_peak_bytes,
      (await Promise.all(nodes.map((n) => rss(n.pid)))).reduce(
        (a, b) => a + b,
        0,
      ),
    );
  } finally {
    sampling = false;
  }
}
let timer;
try {
  await api("/api/login", { token: fixture.token });
  await until(
    async () =>
      (await api("/api/hosts")).filter(
        (h) => nodes.some((n) => n.id === h.id) && h.online && h.snapshot,
      ).length === nodes.length,
  );
  timer = setInterval(sample, 200);
  await sample();
  const queryTimes = [],
    queryStart = performance.now();
  await parallel(
    Array.from(
      { length: nodes.length * 25 },
      (_, i) => nodes[i % nodes.length],
    ),
    async (n) => {
      const start = performance.now();
      const value = await api(`/api/hosts/${n.id}/query`, {
        action: "file.read",
        params: { path: "hello.txt" },
      });
      assert(
        JSON.stringify(value).includes(`${n.name} file evidence`),
        "query crossed host scope",
      );
      queryTimes.push(performance.now() - start);
    },
  );
  report.phases.queries = {
    ...stats(queryTimes),
    wall_ms: performance.now() - queryStart,
  };

  const jobs = [],
    taskTimes = [],
    acceptedTimes = [],
    taskStart = performance.now();
  for (const n of nodes)
    for (let i = 0; i < 8; i++) {
      const content = Buffer.alloc(
        report.bytes_per_file,
        nodes.indexOf(n) * 8 + i,
      );
      const path = `load-${run}-${i}.bin`;
      await writeFile(join(n.root, path), content);
      jobs.push({ n, path, digest: hash(content) });
    }
  await parallel(jobs, async (j) => {
    j.start = performance.now();
    j.task = await api(
      `/api/hosts/${j.n.id}/tasks`,
      { action: "file.download", params: { path: j.path }, confirm: true },
      { "Idempotency-Key": `${run}-${j.path}` },
    );
    acceptedTimes.push(performance.now() - j.start);
  });
  let mixedRunning = true;
  const mixedTimes = [],
    mixedErrors = [];
  const mixedWork = (async () => {
    while (mixedRunning) {
      await parallel(
        nodes,
        async (n) => {
          const start = performance.now();
          try {
            const value = await api(`/api/hosts/${n.id}/query`, {
              action: "file.read",
              params: { path: "hello.txt" },
            });
            assert.equal(
              value.state,
              "succeeded",
              value.error || "query did not succeed",
            );
            assert(JSON.stringify(value).includes(`${n.name} file evidence`));
            mixedTimes.push(performance.now() - start);
          } catch (error) {
            mixedErrors.push(
              String(error.message).replaceAll(
                /hosts\/[a-f0-9]+/g,
                "hosts/<id>",
              ),
            );
          }
        },
        4,
      );
      await delay(500);
    }
  })();
  let outstanding = jobs;
  while (outstanding.length) {
    assert(performance.now() - taskStart < 90000, "task workload timed out");
    const done = new Set();
    await parallel(outstanding, async (j) => {
      const t = await api(`/api/tasks/${j.task.id}`);
      if (["succeeded", "failed", "uncertain"].includes(t.state)) {
        j.final = t;
        done.add(j);
        taskTimes.push(performance.now() - j.start);
        if (t.state === "succeeded") {
          const response = await fetch(
            `${fixture.base}/api/tasks/${t.id}/download`,
            { headers: { Cookie: cookie }, signal: AbortSignal.timeout(30000) },
          );
          assert.equal(response.status, 200);
          assert.equal(
            hash(Buffer.from(await response.arrayBuffer())),
            j.digest,
            "download data mismatch",
          );
        }
      }
    });
    outstanding = outstanding.filter((j) => !done.has(j));
    if (outstanding.length) await delay(150);
  }
  mixedRunning = false;
  await mixedWork;
  report.phases.reads_during_transfers = {
    ...stats(mixedTimes),
    failures: mixedErrors,
  };
  report.phases.downloads = {
    ...stats(taskTimes),
    acceptance: stats(acceptedTimes),
    wall_ms: performance.now() - taskStart,
    succeeded: jobs.filter((j) => j.final.state === "succeeded").length,
    failures: jobs
      .filter((j) => j.final.state !== "succeeded")
      .map((j) => ({ state: j.final.state, error: j.final.error })),
  };

  const uploads = Array.from({ length: nodes.length * 4 }, (_, i) => ({
    n: nodes[i % nodes.length],
    path: `upload-${run}-${i}.bin`,
    content: Buffer.alloc(report.bytes_per_file, i),
  }));
  const uploadTimes = [],
    uploadStart = performance.now();
  await parallel(uploads, async (j) => {
    const start = performance.now();
    const t = await api(
      `/api/hosts/${j.n.id}/uploads?path=${j.path}`,
      j.content,
      { "Idempotency-Key": `${run}-${j.path}`, "X-Confirm-Target": j.n.id },
      true,
    );
    await until(async () => {
      const current = await api(`/api/tasks/${t.id}`);
      assert(
        !["failed", "uncertain"].includes(current.state),
        `upload failed: ${current.error}`,
      );
      return current.state === "succeeded";
    });
    assert.equal(hash(await readFile(join(j.n.root, j.path))), hash(j.content));
    uploadTimes.push(performance.now() - start);
  });
  report.phases.uploads = {
    ...stats(uploadTimes),
    wall_ms: performance.now() - uploadStart,
    retryable_429: backpressure,
  };
  report.before_restart = await api("/api/diagnostics");

  for (const n of nodes) process.kill(n.pid, "SIGSTOP");
  const queued = [];
  for (const n of nodes) {
    const task = await api(
      `/api/hosts/${n.id}/tasks`,
      { action: "file.download", params: { path: "hello.txt" }, confirm: true },
      { "Idempotency-Key": `${run}-restart` },
    );
    assert.equal(task.state, "pending");
    queued.push(task);
  }
  process.kill(fixture.serverPID, "SIGKILL");
  await delay(200);
  const started = performance.now();
  const center = spawn(join(root, "bin/sideria-server"), [], {
    cwd: root,
    env: { ...process.env, ...fixture.serverEnv },
    stdio: "ignore",
  });
  children.push(center);
  fixture.serverPID = center.pid;
  await until(async () => {
    try {
      return (await api("/api/tasks/" + queued[0].id)).id === queued[0].id;
    } catch {
      return false;
    }
  });
  for (const n of nodes) process.kill(n.pid, "SIGCONT");
  await until(
    async () =>
      (await api("/api/hosts")).filter(
        (h) => nodes.some((n) => n.id === h.id) && h.online,
      ).length === nodes.length,
  );
  const connectedMS = performance.now() - started;
  await until(async () => {
    const states = await Promise.all(
      queued.map((t) => api("/api/tasks/" + t.id)),
    );
    states.forEach((t, i) => {
      assert.equal(t.attempt_id, queued[i].attempt_id);
      assert(
        !["failed", "uncertain"].includes(t.state),
        `recovery failure: ${t.error}`,
      );
    });
    return states.every((t) => t.state === "succeeded");
  });
  report.phases.center_restart = {
    connected_ms: connectedMS,
    all_pending_completed_ms: performance.now() - started,
    same_attempts: queued.length,
  };
  report.after_restart = await api("/api/diagnostics");
  report.memory = memory;
  await writeFile(
    join(root, ".run/load-results.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report, null, 2));
  assert.equal(report.phases.downloads.failures.length, 0);
  assert.equal(mixedErrors.length, 0);
} finally {
  clearInterval(timer);
  for (const n of nodes) {
    try {
      process.kill(n.pid, "SIGCONT");
    } catch {}
  }
  for (const child of children)
    if (child.exitCode === null) child.kill("SIGTERM");
}
