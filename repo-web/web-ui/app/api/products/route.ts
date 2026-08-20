import { catalogUrl, proxy } from "@/lib/bff";

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  return proxy(request, catalogUrl("/products"));
}
