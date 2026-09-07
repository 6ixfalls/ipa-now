const API_ROOT = "/api";
export function apiUrl(path: string): string {
  return `${API_ROOT}${path}`;
}
/** One response/error boundary for both JSON requests and raw IPA uploads. */
export async function readResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    let message = "The service could not complete the request.";
    try {
      const body: unknown = await response.json();
      if (
        typeof body === "object" &&
        body !== null &&
        "message" in body &&
        typeof body.message === "string" &&
        body.message
      ) {
        message = body.message;
      }
    } catch {
      /* A proxy or disconnected server may return a non-JSON error. */
    }
    throw new Error(message);
  }
  if (response.status === 204) return undefined as T;
  return response.json();
}
export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return readResponse<T>(await fetch(apiUrl(path), { method: "GET", signal }));
}
export async function post<T>(path: string, body: unknown): Promise<T> {
  return readResponse<T>(
    await fetch(apiUrl(path), {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-IPA-Now": "1" },
      body: JSON.stringify(body),
    }),
  );
}
