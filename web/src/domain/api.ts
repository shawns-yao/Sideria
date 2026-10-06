export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T = any>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  options.signal?.addEventListener("abort", abort, { once: true });
  if (options.signal?.aborted) controller.abort();
  const timer = setTimeout(abort, path === "/api/analyses" ? 125000 : 25000);
  try {
    const response = await fetch(path, {
      ...options,
      signal: controller.signal,
      headers: { "Content-Type": "application/json", ...options.headers },
    });
    const value = await response.json();
    if (!response.ok)
      throw new APIError(response.status, value.error ?? "请求失败");
    return value as T;
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener("abort", abort);
  }
}
export function query(
  host: string,
  action: string,
  params: unknown,
  signal?: AbortSignal,
) {
  return api(`/api/hosts/${host}/query`, {
    method: "POST",
    body: JSON.stringify({ action, params }),
    signal,
  }).then((r) => {
    if (r.error) throw new Error(r.error);
    return r.data;
  });
}
export function task(host: string, action: string, params: unknown) {
  return api(`/api/hosts/${host}/tasks`, {
    method: "POST",
    headers: { "Idempotency-Key": crypto.randomUUID() },
    body: JSON.stringify({ action, params, confirm: true }),
  });
}
