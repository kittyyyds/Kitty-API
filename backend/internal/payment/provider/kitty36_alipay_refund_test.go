//go:build unit

package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/require"
)

func TestKitty36AlipayRefundOperationIdentity(t *testing.T) {
	req := payment.RefundRequest{OrderID: "order_36", Amount: "12.50", OperationID: "operation-a"}
	a := alipayRefundRequestNo(req)
	req.Reason = "retry after timeout"
	require.Equal(t, a, alipayRefundRequestNo(req))
	req.Amount = "12.5"
	require.Equal(t, a, alipayRefundRequestNo(req))
	req.OperationID = "operation-b"
	require.NotEqual(t, a, alipayRefundRequestNo(req), "different equal-amount refunds must not merge")
	req.OrderID = strings.Repeat("x", 64)
	require.LessOrEqual(t, len(alipayRefundRequestNo(req)), 64)
	old := alipayTradeRefund
	t.Cleanup(func() { alipayTradeRefund = old })
	p := &Alipay{client: &alipay.Client{}}
	var received []string
	alipayTradeRefund = func(_ context.Context, _ *alipay.Client, r alipay.TradeRefund) (*alipay.TradeRefundRsp, error) {
		received = append(received, r.OutRequestNo)
		return nil, errors.New("timeout after dispatch")
	}
	for i := 0; i < 2; i++ {
		resp, err := p.Refund(context.Background(), req)
		require.Error(t, err)
		require.NotNil(t, resp)
		require.Equal(t, payment.ProviderStatusPending, resp.Status)
		require.Equal(t, alipayRefundRequestNo(req), resp.RefundID)
	}
	require.Equal(t, received[0], received[1])
}
