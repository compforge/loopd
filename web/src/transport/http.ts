export interface Page<T> {
  data: T[];
}

export function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

export function isAbort(cause: unknown): boolean {
  return cause instanceof Error && cause.name === "AbortError";
}

export async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const timeout = AbortSignal.timeout(30_000);
  const signal = init?.signal ? AbortSignal.any([init.signal, timeout]) : timeout;
  const response = await fetch(path, { ...init, signal });
  if (!response.ok) throw await responseError(response);
  return (await response.json()) as T;
}

export async function responseError(response: Response): Promise<Error> {
  try {
    const value = (await response.json()) as { error?: { message?: string } };
    return new Error(value.error?.message || `loop-server returned ${response.status}`);
  } catch {
    return new Error(`loop-server returned ${response.status}`);
  }
}
