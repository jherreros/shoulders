export interface LokiQueryParams {
  params: URLSearchParams;
  startNs: bigint;
  endNs: bigint;
}

export function buildLokiQueryParams(appName: string, limit: number, sinceSeconds: number, nowMs = Date.now()): LokiQueryParams {
  const endNs = BigInt(nowMs) * 1_000_000n;
  const startNs = endNs - BigInt(sinceSeconds) * 1_000_000_000n;
  const params = new URLSearchParams({
    query: `{app=\"${appName}\"}`,
    limit: String(limit),
    start: startNs.toString(),
    end: endNs.toString(),
    direction: "BACKWARD"
  });
  return { params, startNs, endNs };
}

export function buildTempoTracePath(traceId: string): string {
  return `/api/traces/${traceId}`;
}
