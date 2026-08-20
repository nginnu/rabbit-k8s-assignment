import { orderUrl, proxy } from "@/lib/bff";

export const dynamic = "force-dynamic";

export async function GET(
  request: Request,
  { params }: { params: { id: string } }
) {
  return proxy(request, orderUrl(`/payments/${params.id}/success`));
}
