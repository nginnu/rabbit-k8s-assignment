// Package domain defines payment entities and value objects.
package domain

import "time"

// PaymentStatus represents the state of a payment.
type PaymentStatus string

const (
	StatusPending PaymentStatus = "pending"
	StatusPaid    PaymentStatus = "paid"
	StatusFailed  PaymentStatus = "failed"
)

// PaymentMethod is the settlement channel a client requests at checkout.
type PaymentMethod string

const (
	MethodCard      PaymentMethod = "card"
	MethodTrueMoney PaymentMethod = "truemoney"
	MethodGWallet   PaymentMethod = "gwallet"
	MethodCOD       PaymentMethod = "cod"
)

// validPaymentMethods is the exhaustive set backing the payments.method enum
// in storage/mariadb-init.sql. A value outside this set must be rejected here,
// at the handler — letting it through would fail the enum constraint at the
// DB with a driver error instead of a clean 400.
var validPaymentMethods = map[PaymentMethod]bool{
	MethodCard:      true,
	MethodTrueMoney: true,
	MethodGWallet:   true,
	MethodCOD:       true,
}

// Valid reports whether m is one of the four settlement channels the schema
// and the gateway both know about.
func (m PaymentMethod) Valid() bool {
	return validPaymentMethods[m]
}

// Payment maps to the payments table in MariaDB.
type Payment struct {
	ID         int           `gorm:"primaryKey;autoIncrement"                                                        json:"id"`
	OrderID    int           `gorm:"column:order_id;not null"                                                        json:"order_id"`
	Amount     float64       `gorm:"type:decimal(10,2);not null"                                                     json:"amount"`
	Method     PaymentMethod `gorm:"type:enum('card','truemoney','gwallet','cod');not null;default:card"             json:"method"`
	Status     PaymentStatus `gorm:"type:enum('pending','paid','failed');not null;default:pending"                   json:"status"`
	GatewayRef string        `gorm:"type:varchar(64)"                                                                json:"gateway_ref,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// TableName overrides gorm table name.
func (Payment) TableName() string { return "payments" }

// ChargeResult is the response from the payment gateway.
type ChargeResult struct {
	Status string `json:"status"`
	Ref    string `json:"ref"`
	Error  string `json:"error,omitempty"`
}

// OrderInfo is a subset of order data returned by OrderGateway.Validate.
//
// Amount is order's word on what the order costs — derived from
// products.price, never from the client. ProcessPayment charges this value
// and this value alone; nothing the caller sends can move it.
type OrderInfo struct {
	ID     int     `json:"id"`
	Status string  `json:"status"`
	Amount float64 `json:"amount"`
}
