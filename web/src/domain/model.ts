import { z } from "zod";
export const snapshotSchema = z.object({
  sampled_at: z.string(),
  interval_seconds: z.number().min(1).max(60).optional(),
  received_at: z.string(),
  cpu: z.number().min(0).max(100).nullable(),
  cores: z.number(),
  memory_total: z.number().nonnegative(),
  memory_available: z.number().nonnegative(),
  memory_valid: z.boolean(),
  swap_total: z.number(),
  swap_used: z.number(),
  load: z.array(z.number()).nullable(),
  uptime: z.number(),
  os: z.string(),
  hostname: z.string(),
  disks: z
    .array(
      z.object({
        mount: z.string(),
        total: z.number(),
        used: z.number(),
        inodes_total: z.number(),
        inodes_used: z.number(),
      }),
    )
    .nullable(),
  networks: z
    .array(
      z.object({
        name: z.string(),
        received: z.number(),
        sent: z.number(),
        rx: z.number().nonnegative().nullable(),
        tx: z.number().nonnegative().nullable(),
      }),
    )
    .nullable(),
  errors: z.array(z.string()).nullable(),
});
export const hostSchema = z.object({
  id: z.string(),
  name: z.string(),
  online: z.boolean(),
  last_seen: z.string().nullable(),
  capabilities: z.record(z.string(), z.string()),
  snapshot: snapshotSchema.nullable(),
  history: z.array(snapshotSchema).optional(),
});
export type Snapshot = z.infer<typeof snapshotSchema>;
export type Host = z.infer<typeof hostSchema>;
export type Task = {
  id: string;
  host_id: string;
  action: string;
  params: Record<string, unknown>;
  state: string;
  attempt_id: string;
  created_at: string;
  error: string;
  result: unknown;
};
export function bytes(n?: number | null): string {
  if (n == null || !Number.isFinite(n)) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  while (n >= 1024 && i < 4) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`;
}
export function percent(n?: number | null): string {
  return n == null ? "—" : n.toFixed(1);
}
export function memory(s?: Snapshot | null): number | null {
  return s?.memory_valid && s.memory_total
    ? (100 * (s.memory_total - s.memory_available)) / s.memory_total
    : null;
}
export function time(s?: string | null): string {
  return s
    ? new Date(s).toLocaleTimeString("zh-CN", { hour12: false })
    : "尚未收到";
}
export function stateLabel(s: string): string {
  return (
    (
      {
        pending: "待执行",
        running: "执行中",
        succeeded: "成功",
        failed: "失败",
        uncertain: "状态待确认",
        completed: "分析完成",
        incomplete: "分析未完成",
      } as Record<string, string>
    )[s] ?? s
  );
}
export function stale(h: Host, now = Date.now()): boolean {
  return (
    !h.online ||
    !h.snapshot ||
    now - Date.parse(h.snapshot.sampled_at) >
      Math.max(20000, samplingGap(h.snapshot))
  );
}
export function samplingGap(s?: Snapshot | null): number {
  return (s?.interval_seconds ?? 5) * 3000;
}
export type Point = { at: number; value: number | null };
// Missing values and gaps longer than three configured intervals start a new segment.
export function linePath(
  points: Point[],
  width: number,
  height: number,
  maximum?: number,
  gapMS = 15000,
): string {
  if (points.length < 2) return "";
  const first = points[0]!.at,
    span = points[points.length - 1]!.at - first;
  if (span <= 0) return "";
  const max = maximum ?? Math.max(1, ...points.map((p) => p.value ?? 0));
  let pen = false,
    previous = first;
  return points
    .map((p) => {
      if (p.value == null) {
        pen = false;
        previous = p.at;
        return "";
      }
      const command = pen && p.at - previous <= gapMS ? "L" : "M";
      pen = true;
      previous = p.at;
      return `${command}${(((p.at - first) / span) * width).toFixed(1)},${(height - (p.value / max) * height).toFixed(1)}`;
    })
    .join(" ");
}
