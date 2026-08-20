const FORWARDED_REQUEST_HEADERS = [
  "authorization",
  "content-type",
  "x-chaos-error-rate",
  "x-chaos-latency-ms",
];

const STRIPPED_RESPONSE_HEADERS = new Set([
  "connection",
  "keep-alive",
  "transfer-encoding",
  "upgrade",
  "content-length",
  "content-encoding",
  "set-cookie",
]);

export const TOKEN_COOKIE = "token";

export function bearerFrom(request: Request): string | null {
  const header = request.headers.get("authorization");
  if (header) return header;
  const token = tokenFromCookie(request);
  return token ? `Bearer ${token}` : null;
}

export function tokenFromCookie(request: Request): string | null {
  const cookie = request.headers
    .get("cookie")
    ?.split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${TOKEN_COOKIE}=`));
  if (!cookie) return null;
  const token = cookie.slice(TOKEN_COOKIE.length + 1);
  return token || null;
}

export async function proxy(request: Request, url: string): Promise<Response> {
  const headers = new Headers();
  for (const name of FORWARDED_REQUEST_HEADERS) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  const bearer = bearerFrom(request);
  if (bearer) headers.set("authorization", bearer);

  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  const upstream = await fetch(url, {
    method: request.method,
    headers,
    body: hasBody ? await request.arrayBuffer() : undefined,
    cache: "no-store",
  });

  const responseHeaders = new Headers();
  upstream.headers.forEach((value, name) => {
    if (!STRIPPED_RESPONSE_HEADERS.has(name.toLowerCase())) {
      responseHeaders.set(name, value);
    }
  });

  const body =
    upstream.status === 204 || upstream.status === 304
      ? null
      : await upstream.arrayBuffer();

  return new Response(body, {
    status: upstream.status,
    headers: responseHeaders,
  });
}

export function decodeJwt(token: string): Record<string, unknown> | null {
  try {
    const part = token.split(".")[1];
    if (!part) return null;
    const b64 = part.replace(/-/g, "+").replace(/_/g, "/");
    const pad = b64.length % 4 ? 4 - (b64.length % 4) : 0;
    return JSON.parse(atob(b64 + "=".repeat(pad)));
  } catch {
    return null;
  }
}

export function authUrl(path: string): string {
  return `${process.env.AUTH_URL}${path}`;
}

export function catalogUrl(path: string): string {
  return `${process.env.CATALOG_URL}${path}`;
}

export function orderUrl(path: string): string {
  return `${process.env.ORDER_URL}${path}`;
}
