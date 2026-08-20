import { orderUrl, proxy } from "@/lib/bff";

export async function POST(request: Request) {
  return proxy(request, orderUrl("/payments"));
}
