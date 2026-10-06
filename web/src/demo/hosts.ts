import type { Host, Snapshot } from "../domain/model";
const base = Date.parse("2026-10-06T14:00:00Z");
const samples = [18, 23, 21, 32, 27, 26, 35, 29, 24, 27];
function snapshot(i: number): Snapshot {
  return {
    sampled_at: new Date(base + i * 5000).toISOString(),
    received_at: new Date(base + i * 5000 + 150).toISOString(),
    cpu: samples[i]!,
    cores: 4,
    memory_total: 8 * 1024 ** 3,
    memory_available: 4.6 * 1024 ** 3,
    memory_valid: true,
    swap_total: 2 * 1024 ** 3,
    swap_used: 0,
    load: [0.48, 0.72, 0.66],
    uptime: 1058400,
    os: "Ubuntu 24.04 / x86_64",
    hostname: "observatory-demo",
    disks: [
      {
        mount: "/",
        total: 40 * 1024 ** 3,
        used: 20.8 * 1024 ** 3,
        inodes_total: 2000000,
        inodes_used: 230000,
      },
      {
        mount: "/data",
        total: 100 * 1024 ** 3,
        used: 12 * 1024 ** 3,
        inodes_total: 4000000,
        inodes_used: 40000,
      },
    ],
    networks: [
      {
        name: "eth0",
        received: 92000000,
        sent: 34000000,
        rx: samples[i]! * 20000,
        tx: samples[9 - i]! * 5000,
      },
      { name: "lo", received: 10000, sent: 10000, rx: 0, tx: 0 },
    ],
    errors: [],
  };
}
export const demoHosts: Host[] = [
  {
    id: "demo-online",
    name: "Beijing-01 · 演示",
    online: true,
    last_seen: snapshot(9).sampled_at,
    capabilities: {
      files: "未接入",
      docker: "未接入",
      git: "未接入",
      terminal: "未接入",
    },
    snapshot: snapshot(9),
    history: samples.map((_, i) => snapshot(i)),
  },
  {
    id: "demo-offline",
    name: "Singapore-02 · 失联样本",
    online: false,
    last_seen: snapshot(3).sampled_at,
    capabilities: {},
    snapshot: snapshot(3),
    history: samples.slice(0, 4).map((_, i) => snapshot(i)),
  },
  {
    id: "demo-empty",
    name: "New host · 无数据",
    online: false,
    last_seen: null,
    capabilities: {},
    snapshot: null,
    history: [],
  },
];
