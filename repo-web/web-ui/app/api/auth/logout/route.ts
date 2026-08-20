import { TOKEN_COOKIE } from "@/lib/bff";

export async function POST() {
  const headers = new Headers();
  headers.append(
    "Set-Cookie",
    `${TOKEN_COOKIE}=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax`
  );
  return new Response(null, { status: 204, headers });
}
