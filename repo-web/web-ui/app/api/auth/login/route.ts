import { authUrl, proxy, TOKEN_COOKIE } from "@/lib/bff";

export async function POST(request: Request) {
  const res = await proxy(request, authUrl("/auth/login"));
  const body = await res.text();

  if (!res.ok) {
    return new Response(body, { status: res.status, headers: res.headers });
  }

  let token: string | undefined;
  let expiresAt: string | undefined;
  try {
    const data = JSON.parse(body);
    token = data.token;
    expiresAt = data.expires_at;
  } catch {}

  const headers = new Headers(res.headers);
  if (token) {
    const parts = [
      `${TOKEN_COOKIE}=${token}`,
      "Path=/",
      "HttpOnly",
      "Secure",
      "SameSite=Lax",
    ];
    const expires = expiresAt ? Date.parse(expiresAt) : NaN;
    if (!Number.isNaN(expires)) {
      const maxAge = Math.max(0, Math.floor((expires - Date.now()) / 1000));
      parts.push(`Max-Age=${maxAge}`);
    }
    headers.append("Set-Cookie", parts.join("; "));
  }

  return new Response(body, { status: res.status, headers });
}
