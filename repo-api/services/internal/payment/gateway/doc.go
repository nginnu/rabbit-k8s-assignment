// Package gateway provides payment's three outbound dependencies as
// implementations of the interfaces usecase.PaymentUsecase depends on:
// order (in-process, see order_adapter.go), the external payment gateway,
// and notification (both still real HTTP clients — genuinely separate
// services, unlike order).
package gateway

import "go.opentelemetry.io/otel"

// tracer is shared by every file in this package. It used to be named
// "payment-svc" back when order.go's OrderClient called a separate
// payment-svc process; renamed to match the <context>/<layer> convention
// the order package already uses now that there is no standalone
// payment-svc for the old name to refer to.
var tracer = otel.Tracer("payment/gateway")
