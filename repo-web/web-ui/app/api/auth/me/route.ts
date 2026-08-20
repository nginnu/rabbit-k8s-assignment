import { decodeJwt, tokenFromCookie } from "@/lib/bff";

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const token = tokenFromCookie(request);
  if (!token) {
    return Response.json({ error: "not logged in" }, { status: 401 });
  }

  const claims = decodeJwt(token);
  if (!claims || typeof claims.uid !== "number") {
    return Response.json({ error: "invalid token" }, { status: 401 });
  }

  return Response.json({
    user_id: claims.uid,
    username: typeof claims.usr === "string" ? claims.usr : null,
    session_id: typeof claims.sid === "string" ? claims.sid : null,
    expires_at:
      typeof claims.exp === "number"
        ? new Date(claims.exp * 1000).toISOString()
        : null,
  });
}
