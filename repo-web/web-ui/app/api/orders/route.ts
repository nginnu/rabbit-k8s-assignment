import { orderUrl, proxy } from "@/lib/bff";

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  return proxy(request, orderUrl("/orders"));
}

export async function POST(request: Request) {
  return proxy(request, orderUrl("/orders"));
}
