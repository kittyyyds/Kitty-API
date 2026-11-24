//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestKitty36RefundOperationPersistsAndReusesAcrossRetry(t *testing.T) {
	ctx := context.Background()
	c := newPaymentConfigServiceTestClient(t)
	u, err := c.User.Create().SetEmail("refund-operation@example.test").SetPasswordHash("fixture").SetUsername("fixture").Save(ctx)
	require.NoError(t, err)
	o, err := c.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName(u.Username).SetAmount(100).SetPayAmount(100).SetFeeRate(0).SetRechargeCode("OP36").SetOutTradeNo("op36").SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo("fixture_trade").SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusCompleted).SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("example.test").Save(ctx)
	require.NoError(t, err)
	s := &PaymentService{entClient: c}
	plan := func() *RefundPlan { return &RefundPlan{OrderID: o.ID, Order: o, RefundAmount: 40, GatewayAmount: 40} }
	p := plan()
	require.NoError(t, s.claimRefundOperation(ctx, p))
	require.NotEmpty(t, p.refundOperationID)
	// Only one caller can own the order at a time.
	require.Error(t, s.claimRefundOperation(ctx, plan()))
	require.NoError(t, c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusCompleted).Exec(ctx))
	retry := plan()
	require.NoError(t, s.claimRefundOperation(ctx, retry))
	require.Equal(t, p.refundOperationID, retry.refundOperationID)
	require.NoError(t, c.PaymentOrder.UpdateOneID(o.ID).SetStatus(OrderStatusCompleted).Exec(ctx))
	different := plan()
	different.GatewayAmount = 41
	require.Error(t, s.claimRefundOperation(ctx, different), "ambiguous earlier operation cannot be changed")
	stored, err := c.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, stored.Status)
	// An authoritative failed query closes the old operation. A new legitimate
	// request of the same amount then receives a distinct durable identity.
	_, err = c.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("REFUND_OPERATION_FINALIZED").SetDetail(`{"status":"failed"}`).SetOperator("test").Save(ctx)
	require.NoError(t, err)
	next := plan()
	require.NoError(t, s.claimRefundOperation(ctx, next))
	require.NotEqual(t, p.refundOperationID, next.refundOperationID)
}
