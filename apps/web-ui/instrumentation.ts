import { registerOTel } from "@vercel/otel";

export function register() {
  registerOTel({
    serviceName: "platform-local-web-ui",
  });
}
