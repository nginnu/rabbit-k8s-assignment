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

// Payment maps to the payments table in MariaDB.
type Payment struct {
	ID         int           `gorm:"primaryKey;autoIncrement"                                         json:"id"`
	OrderID    int           `gorm:"column:order_id;not null"                                         json:"order_id"`
	Amount     float64       `gorm:"type:decimal(10,2);not null"                                      json:"amount"`
	Status     PaymentStatus `gorm:"type:enum('pending','paid','failed');not null;default:pending"    json:"status"`
	GatewayRef string        `gorm:"type:varchar(64)"                                                 json:"gateway_ref,omitempty"`
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
