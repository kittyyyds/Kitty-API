package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type refundOperationAudit struct {
	OperationID  string `json:"operationID"`
	Amount       string `json:"gatewayAmount"`
	Currency     string `json:"currency"`
	RefundAmount string `json:"refundAmount"`
}

// Claim and durable request identity are committed together, before deductions
// or network calls. An ambiguous failure retains its identity across retries.
func (s *PaymentService) claimRefundOperation(ctx context.Context, p *RefundPlan) error {
	if p == nil || p.Order == nil {
		return fmt.Errorf("refund plan/order is nil")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin refund claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	n, err := tx.PaymentOrder.Update().Where(paymentorder.IDEQ(p.OrderID), paymentorder.StatusIn(OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed), paymentorder.InvoiceRequestIDIsNil()).SetStatus(OrderStatusRefunding).Save(ctx)
	if err != nil {
		return fmt.Errorf("lock refund: %w", err)
	}
	if n == 0 {
		return infraerrors.Conflict("CONFLICT", "order status changed")
	}
	amount, currency := formatGatewayRefundAmount(p.GatewayAmount, p.Order), PaymentOrderCurrency(p.Order)
	refundAmount := strconv.FormatFloat(p.RefundAmount, 'g', -1, 64)
	last, err := tx.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_OPERATION")).Order(paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return fmt.Errorf("read refund operation: %w", err)
	}
	var operation refundOperationAudit
	if last != nil {
		closed, err := tx.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_OPERATION_FINALIZED"), paymentauditlog.IDGT(last.ID)).Exist(ctx)
		if err != nil {
			return fmt.Errorf("read refund operation state: %w", err)
		}
		if !closed {
			if err := json.Unmarshal([]byte(last.Detail), &operation); err != nil || operation.OperationID == "" {
				return fmt.Errorf("invalid persisted refund operation")
			}
			if operation.Amount != amount || operation.Currency != currency || operation.RefundAmount != refundAmount {
				return infraerrors.Conflict("REFUND_OPERATION_UNRESOLVED", "previous refund outcome is unresolved; verify it before changing the refund amount")
			}
		}
	}
	if operation.OperationID == "" {
		operation = refundOperationAudit{OperationID: uuid.NewString(), Amount: amount, Currency: currency, RefundAmount: refundAmount}
		if err := createPaymentAuditLogTx(ctx, tx.Client(), p.OrderID, "REFUND_OPERATION", map[string]any{"operationID": operation.OperationID, "gatewayAmount": operation.Amount, "currency": operation.Currency, "refundAmount": operation.RefundAmount}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refund claim: %w", err)
	}
	p.refundOperationID = operation.OperationID
	return nil
}
