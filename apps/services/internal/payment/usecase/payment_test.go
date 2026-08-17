package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/gateway"

	"go.opentelemetry.io/otel/baggage"
)

// The fakes record every call in one shared log, because what these tests pin
// down is the saga's order, not just its outcome: a step that silently stops
// running (say, the notify call after a refactor) is a regression a return
// value alone cannot show. The usecase's dependencies are interfaces for
// exactly this — the concrete gorm/HTTP implementations never know they are
// implementing one.
type callLog struct{ calls []string }

func (l *callLog) call(name string) { l.calls = append(l.calls, name) }

func (l *callLog) has(name string) bool {
	for _, c := range l.calls {
		if c == name {
			return true
		}
	}
	return false
}

type fakeRepo struct {
	log       *callLog
	createErr error
	updateErr error
	find      *domain.Payment
	findErr   error
	// statusUpdates records every UpdateStatus call, so the failed-vs-paid
	// transitions can be asserted, not just that "an update happened".
	statusUpdates []struct {
		id     int
		status domain.PaymentStatus
		ref    string
	}
	// gotMethod records what CreatePending was actually handed, so a test can
	// assert the method that reaches the DB matches what the client asked for.
	gotMethod domain.PaymentMethod
}

func (f *fakeRepo) CreatePending(_ context.Context, orderID int, amount float64, method domain.PaymentMethod) (*domain.Payment, error) {
	f.log.call("repo.create_pending")
	f.gotMethod = method
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &domain.Payment{ID: 42, OrderID: orderID, Amount: amount, Method: method, Status: domain.StatusPending}, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id int, status domain.PaymentStatus, ref string) error {
	f.log.call("repo.update_status")
	f.statusUpdates = append(f.statusUpdates, struct {
		id     int
		status domain.PaymentStatus
		ref    string
	}{id, status, ref})
	return f.updateErr
}

func (f *fakeRepo) FindByID(_ context.Context, _ int) (*domain.Payment, error) {
	f.log.call("repo.find_by_id")
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.find, nil
}

type fakeOrder struct {
	log         *callLog
	validateErr error
	markPaidErr error
	// amount is what order-svc says this order costs — the only source
	// ProcessPayment reads a price from, since ProcessPaymentInput carries
	// none.
	amount float64
}

func (f *fakeOrder) Validate(_ context.Context, _ int) (*domain.OrderInfo, error) {
	f.log.call("order.validate")
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return &domain.OrderInfo{ID: 7, Status: "pending", Amount: f.amount}, nil
}

func (f *fakeOrder) MarkPaid(_ context.Context, _ int) error {
	f.log.call("order.mark_paid")
	return f.markPaidErr
}

type fakeCharge struct {
	log    *callLog
	err    error
	result *domain.ChargeResult
	// gotAmount records what ProcessPayment actually sent to Charge, so a
	// test can assert it traces back to the order, not the caller.
	gotAmount float64
	// gotMethod records what ProcessPayment actually sent to Charge.
	gotMethod domain.PaymentMethod
}

func (f *fakeCharge) Charge(_ context.Context, amount float64, method domain.PaymentMethod, _ gateway.ChaosHeaders) (*domain.ChargeResult, error) {
	f.log.call("gateway.charge")
	f.gotAmount = amount
	f.gotMethod = method
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &domain.ChargeResult{Status: "ok", Ref: "pay-ref-1"}, nil
}

type fakeNotify struct {
	log *callLog
	err error
	// lastArgs records the payload, because a notification that fires with the
	// wrong ids is worse than none: the checkout proof greps by order_id.
	lastPaymentID int
	lastOrderID   int
	lastAmount    float64
}

func (f *fakeNotify) Notify(_ context.Context, paymentID, orderID int, amount float64, _ int) error {
	f.log.call("notify.send")
	f.lastPaymentID, f.lastOrderID, f.lastAmount = paymentID, orderID, amount
	return f.err
}

// newUsecase wires the four fakes around one call log. fakeOrder defaults to
// answering 3290 for order 7's price, the same fixture used throughout this
// file — ProcessPaymentInput has nothing to override it with.
func newUsecase() (PaymentRepository, OrderGateway, ChargeGateway, Notifier, *callLog) {
	log := &callLog{}
	return &fakeRepo{log: log}, &fakeOrder{log: log, amount: 3290}, &fakeCharge{log: log}, &fakeNotify{log: log}, log
}

func input() ProcessPaymentInput {
	return ProcessPaymentInput{OrderID: 7, UserID: 1, Method: domain.MethodCard}
}

func TestMain(m *testing.M) {
	// The usecase logs every step; keep test output about verdicts, not saga
	// narration. otel stays a no-op — the global tracer needs no Init.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.Run()
}

func TestProcessPaymentHappyPath(t *testing.T) {
	repo, order, charge, notify, log := newUsecase()
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != domain.StatusPaid {
		t.Errorf("status = %q, want %q", out.Status, domain.StatusPaid)
	}
	if out.PaymentID != 42 {
		t.Errorf("payment id = %d, want 42", out.PaymentID)
	}
	if out.GatewayRef != "pay-ref-1" {
		t.Errorf("gateway ref = %q, want pay-ref-1", out.GatewayRef)
	}

	// The order of the saga is the contract: every downstream service and the
	// DB proof depend on it.
	want := []string{
		"order.validate",
		"repo.create_pending",
		"gateway.charge",
		"repo.update_status",
		"order.mark_paid",
		"notify.send",
	}
	if fmt.Sprint(log.calls) != fmt.Sprint(want) {
		t.Errorf("call order = %v\nwant           %v", log.calls, want)
	}
	fr := repo.(*fakeRepo)
	if len(fr.statusUpdates) != 1 || fr.statusUpdates[0].status != domain.StatusPaid {
		t.Errorf("status updates = %+v, want one paid", fr.statusUpdates)
	}
	if fr.statusUpdates[0].ref != "pay-ref-1" {
		t.Errorf("paid update ref = %q, want the gateway ref", fr.statusUpdates[0].ref)
	}
	fn := notify.(*fakeNotify)
	if fn.lastOrderID != 7 || fn.lastPaymentID != 42 {
		t.Errorf("notify payload ids = (%d,%d), want (42,7)", fn.lastPaymentID, fn.lastOrderID)
	}
}

// This is the headline fix under test. ProcessPaymentInput carries no Amount
// field at all — there is structurally nothing for a caller to override —
// so the only way CreatePending and Charge can see a price is through
// order.Validate. Changing what the fake order-svc answers with, and nothing
// else, must change what gets charged.
func TestProcessPaymentChargesOrderDerivedAmountNotClientInput(t *testing.T) {
	repo, order, charge, notify, _ := newUsecase()
	order.(*fakeOrder).amount = 4500 // order-svc's answer for this order
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != domain.StatusPaid {
		t.Fatalf("status = %q, want paid", out.Status)
	}

	fc := charge.(*fakeCharge)
	if fc.gotAmount != 4500 {
		t.Errorf("charged amount = %v, want 4500 from order-svc", fc.gotAmount)
	}
	fr := repo.(*fakeRepo)
	if len(fr.statusUpdates) != 1 {
		t.Fatalf("status updates = %+v, want exactly one", fr.statusUpdates)
	}
	fn := notify.(*fakeNotify)
	if !fn.log.has("notify.send") {
		t.Fatalf("notify never ran")
	}
	if fn.lastAmount != 4500 {
		t.Errorf("notify amount = %v, want 4500 — the announced amount must match what was actually charged", fn.lastAmount)
	}
}

func TestProcessPaymentValidateFailsBeforeAnyWrite(t *testing.T) {
	_, order, charge, notify, log := newUsecase()
	order.(*fakeOrder).validateErr = errors.New(`order status is "paid"`)
	uc := New(&fakeRepo{log: log}, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err == nil {
		t.Fatal("expected error")
	}
	if out != nil {
		t.Errorf("output = %+v, want nil — nothing was created", out)
	}
	// The whole point of validating first: a rejected order must not leave a
	// pending payment row behind, and the gateway must never be charged.
	if log.has("repo.create_pending") {
		t.Errorf("payment row created after a failed validate: %v", log.calls)
	}
	if log.has("gateway.charge") || log.has("notify.send") {
		t.Errorf("charge/notify ran after a failed validate: %v", log.calls)
	}
}

func TestProcessPaymentCreatePendingFails(t *testing.T) {
	repo, order, charge, notify, log := newUsecase()
	repo.(*fakeRepo).createErr = errors.New("db down")
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err == nil {
		t.Fatal("expected error")
	}
	if out != nil {
		t.Errorf("output = %+v, want nil", out)
	}
	if log.has("gateway.charge") {
		t.Errorf("gateway charged without a payment record: %v", log.calls)
	}
}

func TestProcessPaymentChargeFailsMarksFailed(t *testing.T) {
	repo, order, charge, notify, log := newUsecase()
	charge.(*fakeCharge).err = errors.New("gateway declined")
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err == nil {
		t.Fatal("expected error — the handler maps this to 402")
	}
	// Non-nil output with failed status is the 402 contract: the client needs
	// the payment_id to show which attempt failed.
	if out == nil || out.Status != domain.StatusFailed {
		t.Fatalf("output = %+v, want failed status with payment id", out)
	}
	fr := repo.(*fakeRepo)
	if len(fr.statusUpdates) != 1 || fr.statusUpdates[0].status != domain.StatusFailed {
		t.Fatalf("status updates = %+v, want exactly one failed", fr.statusUpdates)
	}
	if fr.statusUpdates[0].ref != "" {
		t.Errorf("failed update ref = %q, want empty", fr.statusUpdates[0].ref)
	}
	// No point announcing or settling a payment that did not happen.
	if log.has("notify.send") {
		t.Errorf("notify ran for a failed charge: %v", log.calls)
	}
	if log.has("order.mark_paid") {
		t.Errorf("order marked paid after failed charge: %v", log.calls)
	}
}

// cod is not simulated by a special branch in the usecase — there isn't
// one. It goes through the exact same "gateway returned an error" path as
// any declined charge (fakeCharge.err stands in for the 402
// payment-gateway answers for real, per gateway.PaymentGatewayClient.Charge
// treating non-200 as an error). What this test pins down is the outcome
// the storefront and the observability stack both depend on: the payment
// row lands on failed and the order is never touched, so a repeated cod
// request reproduces the same failure every time — not a fresh 500 or a
// half-settled order.
func TestProcessPaymentCODAlwaysFails(t *testing.T) {
	repo, order, charge, notify, log := newUsecase()
	charge.(*fakeCharge).err = errors.New("payment-gateway returned 402: cod settlement is not supported by this gateway")
	uc := New(repo, order, charge, notify)

	in := input()
	in.Method = domain.MethodCOD

	out, err := uc.ProcessPayment(context.Background(), in)
	if err == nil {
		t.Fatal("expected error for cod — the handler maps this to 402")
	}
	if out == nil || out.Status != domain.StatusFailed {
		t.Fatalf("output = %+v, want failed status", out)
	}

	fc := charge.(*fakeCharge)
	if fc.gotMethod != domain.MethodCOD {
		t.Errorf("gateway saw method %q, want cod", fc.gotMethod)
	}

	fr := repo.(*fakeRepo)
	if len(fr.statusUpdates) != 1 || fr.statusUpdates[0].status != domain.StatusFailed {
		t.Fatalf("payments.status = %+v, want exactly one failed", fr.statusUpdates)
	}
	// order.mark_paid must never run: orders.status stays wherever Validate
	// found it (pending) — a cod attempt must not move it.
	if log.has("order.mark_paid") {
		t.Errorf("order marked paid after a cod decline: %v", log.calls)
	}
	if log.has("notify.send") {
		t.Errorf("notify ran for a cod decline: %v", log.calls)
	}
}

func TestProcessPaymentUpdatePaidFails(t *testing.T) {
	repo, order, charge, notify, log := newUsecase()
	repo.(*fakeRepo).updateErr = errors.New("db down")
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err == nil {
		t.Fatal("expected error — money moved but the record does not say so")
	}
	if out != nil {
		t.Errorf("output = %+v, want nil", out)
	}
	// The gateway was charged, so nothing downstream may run on a lie.
	if log.has("order.mark_paid") || log.has("notify.send") {
		t.Errorf("downstream ran after a failed paid update: %v", log.calls)
	}
}

func TestProcessPaymentMarkPaidFailureIsNonFatal(t *testing.T) {
	repo, order, charge, notify, log := newUsecase()
	order.(*fakeOrder).markPaidErr = errors.New("order-svc down")
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	if err != nil {
		t.Fatalf("a failed order-svc update must not fail a cleared payment: %v", err)
	}
	if out.Status != domain.StatusPaid {
		t.Errorf("status = %q, want paid", out.Status)
	}
	if !log.has("notify.send") {
		t.Errorf("notify skipped after non-fatal mark-paid failure: %v", log.calls)
	}
}

func TestProcessPaymentNotifyFailureIsNonFatal(t *testing.T) {
	repo, order, charge, notify, _ := newUsecase()
	notify.(*fakeNotify).err = errors.New("notification down")
	uc := New(repo, order, charge, notify)

	out, err := uc.ProcessPayment(context.Background(), input())
	// The contract the comment in ProcessPayment states: the payment already
	// cleared, so a broken notification must never fail a checkout that
	// succeeded.
	if err != nil {
		t.Fatalf("notify failure failed the checkout: %v", err)
	}
	if out.Status != domain.StatusPaid || out.GatewayRef != "pay-ref-1" {
		t.Errorf("output = %+v, want paid with ref", out)
	}
}

// withOrderBaggage is tested directly rather than through ProcessPayment, so a
// baggage regression fails with the exact member that went wrong instead of a
// downstream service mysteriously misattributing the flow.

func TestWithOrderBaggage(t *testing.T) {
	user, err := baggage.NewMember("user.id", "u1")
	if err != nil {
		t.Fatalf("build user.id member: %v", err)
	}
	stale, err := baggage.NewMember("order.id", "7")
	if err != nil {
		t.Fatalf("build stale order.id member: %v", err)
	}
	base, err := baggage.New(user, stale)
	if err != nil {
		t.Fatalf("build base baggage: %v", err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), base)

	got := baggage.FromContext(withOrderBaggage(ctx, 42))

	// user.id must survive: it was set by Auth, and dropping it here would
	// make every downstream service blame an anonymous user.
	if m := got.Member("user.id"); m.Value() != "u1" {
		t.Errorf("user.id = %q, want %q (existing baggage dropped)", m.Value(), "u1")
	}
	// order.id must be overwritten, not duplicated: a stale order.id from an
	// earlier request in the same trace would misattribute the whole flow.
	if m := got.Member("order.id"); m.Value() != "42" {
		t.Errorf("order.id = %q, want %q (not added/overwritten)", m.Value(), "42")
	}
}

func TestWithOrderBaggageFromEmptyContext(t *testing.T) {
	got := baggage.FromContext(withOrderBaggage(context.Background(), 5))
	if m := got.Member("order.id"); m.Value() != "5" {
		t.Errorf("order.id = %q, want %q", m.Value(), "5")
	}
}

func TestGetPayment(t *testing.T) {
	repo, order, charge, notify, _ := newUsecase()
	repo.(*fakeRepo).find = &domain.Payment{ID: 42, Status: domain.StatusPaid}
	uc := New(repo, order, charge, notify)

	p, err := uc.GetPayment(context.Background(), 42, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status != domain.StatusPaid {
		t.Errorf("status = %q, want paid", p.Status)
	}

	repo.(*fakeRepo).findErr = errors.New("not found")
	if _, err := uc.GetPayment(context.Background(), 99, 1); err == nil {
		t.Error("expected error for missing payment")
	}
}
