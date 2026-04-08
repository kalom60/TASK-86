package services

import (
	"database/sql"
	"errors"
	"fmt"

	"w2t86/internal/models"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
)

// OrderService orchestrates all order-lifecycle, return, and inventory operations.
type OrderService struct {
	orderRepo    *repository.OrderRepository
	materialRepo *repository.MaterialRepository
}

// NewOrderService creates an OrderService wired to the given repositories.
func NewOrderService(or *repository.OrderRepository, mr *repository.MaterialRepository) *OrderService {
	return &OrderService{orderRepo: or, materialRepo: mr}
}

// ---------------------------------------------------------------
// Order placement
// ---------------------------------------------------------------

// PlaceOrder validates that every requested material exists and has sufficient
// stock, then delegates to OrderRepository.Create.
func (s *OrderService) PlaceOrder(userID int64, items []repository.OrderItemInput) (*models.Order, error) {
	if len(items) == 0 {
		return nil, errors.New("service: PlaceOrder: cart is empty")
	}

	for _, it := range items {
		if it.Qty <= 0 {
			return nil, fmt.Errorf("service: PlaceOrder: quantity must be positive for material %d", it.MaterialID)
		}
		mat, err := s.materialRepo.GetByID(it.MaterialID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("service: PlaceOrder: material %d not found", it.MaterialID)
			}
			return nil, fmt.Errorf("service: PlaceOrder: fetch material %d: %w", it.MaterialID, err)
		}
		if mat.Status != "active" {
			return nil, fmt.Errorf("service: PlaceOrder: material %q is not available for ordering", mat.Title)
		}
		if mat.AvailableQty < it.Qty {
			return nil, fmt.Errorf("service: PlaceOrder: insufficient stock for %q (available: %d, requested: %d)",
				mat.Title, mat.AvailableQty, it.Qty)
		}
	}

	order, err := s.orderRepo.Create(userID, items)
	if err != nil {
		return nil, fmt.Errorf("service: PlaceOrder: %w", err)
	}

	observability.Orders.Info("order placed", "order_id", order.ID, "user_id", userID, "item_count", len(items), "total_amount", order.TotalAmount)
	observability.M.OrdersCreated.Add(1)

	return order, nil
}

// ---------------------------------------------------------------
// Student actions
// ---------------------------------------------------------------

// ConfirmPayment transitions an order from pending_payment → pending_shipment.
// Only the order owner may confirm payment.
func (s *OrderService) ConfirmPayment(orderID, userID int64) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return fmt.Errorf("service: ConfirmPayment: %w", err)
	}
	if order.UserID != userID {
		return errors.New("service: ConfirmPayment: not authorized")
	}
	if order.Status != "pending_payment" {
		return fmt.Errorf("service: ConfirmPayment: order is %q, expected pending_payment", order.Status)
	}
	if err := s.orderRepo.Transition(orderID, userID, "pending_shipment", "payment confirmed", s.materialRepo); err != nil {
		return err
	}
	observability.Orders.Info("payment confirmed", "order_id", orderID, "user_id", userID)
	return nil
}

// CancelOrder cancels an order. Business rules:
//   - Students can only cancel their own orders when in pending_payment.
//   - Clerks cannot cancel orders.
//   - Admins/instructors can cancel pending_payment or pending_shipment orders.
//   - Only admins can cancel in_transit orders.
func (s *OrderService) CancelOrder(orderID, actorID int64, role string) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return fmt.Errorf("service: CancelOrder: %w", err)
	}

	switch role {
	case "student":
		if order.UserID != actorID {
			return errors.New("service: CancelOrder: not authorized")
		}
		if order.Status != "pending_payment" {
			return fmt.Errorf("service: CancelOrder: students may only cancel orders in pending_payment (current: %q)", order.Status)
		}
	case "admin", "instructor":
		if order.Status == "in_transit" && role != "admin" {
			return errors.New("service: CancelOrder: only admins can cancel in-transit orders")
		}
		if order.Status == "completed" {
			return errors.New("service: CancelOrder: completed orders cannot be canceled")
		}
		if order.Status == "canceled" {
			return errors.New("service: CancelOrder: order is already canceled")
		}
	case "clerk":
		return errors.New("service: CancelOrder: clerks are not permitted to cancel orders")
	default:
		return errors.New("service: CancelOrder: unknown role")
	}

	note := fmt.Sprintf("canceled by %s (id=%d)", role, actorID)
	if err := s.orderRepo.Transition(orderID, actorID, "canceled", note, s.materialRepo); err != nil {
		return err
	}
	observability.Orders.Info("order canceled", "order_id", orderID, "actor_id", actorID, "role", role, "reason", "user_requested")
	observability.M.OrdersCanceled.Add(1)
	return nil
}

// ---------------------------------------------------------------
// Clerk actions
// ---------------------------------------------------------------

// MarkShipped transitions an order from pending_shipment → in_transit.
// Only clerks (or admins) may call this.
func (s *OrderService) MarkShipped(orderID, actorID int64) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return fmt.Errorf("service: MarkShipped: %w", err)
	}
	if order.Status != "pending_shipment" {
		return fmt.Errorf("service: MarkShipped: order is %q, expected pending_shipment", order.Status)
	}
	return s.orderRepo.Transition(orderID, actorID, "in_transit", "order shipped", s.materialRepo)
}

// MarkDelivered transitions an order from in_transit → completed.
// Only clerks (or admins) may call this.
func (s *OrderService) MarkDelivered(orderID, actorID int64) error {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return fmt.Errorf("service: MarkDelivered: %w", err)
	}
	if order.Status != "in_transit" {
		return fmt.Errorf("service: MarkDelivered: order is %q, expected in_transit", order.Status)
	}
	return s.orderRepo.Transition(orderID, actorID, "completed", "order delivered", s.materialRepo)
}

// ---------------------------------------------------------------
// Returns / Exchanges / Refunds
// ---------------------------------------------------------------

// RequestReturn submits a return/exchange/refund request for an order.
// Rules:
//   - The order must belong to the requesting user.
//   - Return/exchange: allowed only within 14 days of order completion.
//   - Refund: also allowed only within 14 days; requires manager role enforcement
//     at the handler layer (admin or instructor).
//   - Only one pending request may be open per order at a time.
//   - For exchange requests, replacementMaterialID identifies the desired
//     replacement item; it is stored and validated at approval time.
func (s *OrderService) RequestReturn(orderID, userID int64, reqType, reason string, replacementMaterialID *int64) (*models.ReturnRequest, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return nil, fmt.Errorf("service: RequestReturn: %w", err)
	}
	if order.UserID != userID {
		return nil, errors.New("service: RequestReturn: not authorized")
	}
	if order.Status != "completed" {
		return nil, errors.New("service: RequestReturn: returns are only allowed on completed orders")
	}

	switch reqType {
	case "return", "exchange", "refund":
		// valid types
	default:
		return nil, fmt.Errorf("service: RequestReturn: invalid type %q (must be return, exchange, or refund)", reqType)
	}

	// Enforce 14-day window.
	if !repository.WithinReturnWindow(order.UpdatedAt) {
		return nil, errors.New("service: RequestReturn: the 14-day return window has expired")
	}

	// Check for an already-pending request on this order.
	existing, err := s.orderRepo.GetReturnRequestsByUser(userID)
	if err != nil {
		return nil, fmt.Errorf("service: RequestReturn: check existing: %w", err)
	}
	for _, rr := range existing {
		if rr.OrderID == orderID && rr.Status == "pending" {
			return nil, errors.New("service: RequestReturn: a pending return request already exists for this order")
		}
	}

	rr, err := s.orderRepo.CreateReturnRequest(orderID, userID, reqType, reason, replacementMaterialID)
	if err != nil {
		return nil, fmt.Errorf("service: RequestReturn: %w", err)
	}
	observability.Orders.Info("return requested", "order_id", orderID, "type", reqType)
	return rr, nil
}

// ApproveReturn approves a return request.
// Only admins and instructors (managers) may approve.
// For exchange requests, the replacement_material_id must be set on the request
// and the replacement material must have sufficient available stock.
// When the request type is "refund" or "return", a financial_transaction record
// is created for traceability.
func (s *OrderService) ApproveReturn(requestID, actorID int64, role string) error {
	if role != "admin" && role != "instructor" {
		return errors.New("service: ApproveReturn: only managers (admin/instructor) may approve return requests")
	}
	rr, err := s.orderRepo.GetReturnRequestByID(requestID)
	if err != nil {
		return fmt.Errorf("service: ApproveReturn: %w", err)
	}
	if rr.Status != "pending" {
		return fmt.Errorf("service: ApproveReturn: request is already %q", rr.Status)
	}

	// For exchange requests, verify replacement inventory before approving.
	if rr.Type == "exchange" {
		if rr.ReplacementMaterialID == nil {
			return errors.New("service: ApproveReturn: exchange request has no replacement material specified")
		}
		replacement, err := s.materialRepo.GetByID(*rr.ReplacementMaterialID)
		if err != nil {
			return fmt.Errorf("service: ApproveReturn: load replacement material: %w", err)
		}
		if replacement.AvailableQty < 1 {
			return fmt.Errorf("service: ApproveReturn: replacement material %q has no available stock", replacement.Title)
		}
	}

	if err := s.orderRepo.ResolveReturn(requestID, actorID, true); err != nil {
		return err
	}

	// Record a financial transaction for refund or return types.
	if rr.Type == "refund" || rr.Type == "return" {
		// Look up the order amount for the refund value.
		amount := 0.0
		if order, oErr := s.orderRepo.GetByID(rr.OrderID); oErr == nil {
			amount = order.TotalAmount
		}
		txType := "refund"
		note := "approved " + rr.Type + " request"
		rrID := requestID
		oID := rr.OrderID
		if _, ftErr := s.orderRepo.CreateFinancialTransaction(
			&oID, &rrID, txType, amount, actorID, note,
		); ftErr != nil {
			// Non-fatal: log but do not fail the approval.
			observability.Orders.Warn("financial transaction record failed", "request_id", requestID, "error", ftErr)
		}
	}

	observability.Orders.Info("return approved", "request_id", requestID, "actor_id", actorID, "role", role, "type", rr.Type)
	return nil
}

// RejectReturn rejects a return request.
// Clerks, admins, and instructors may reject.
func (s *OrderService) RejectReturn(requestID, actorID int64) error {
	rr, err := s.orderRepo.GetReturnRequestByID(requestID)
	if err != nil {
		return fmt.Errorf("service: RejectReturn: %w", err)
	}
	if rr.Status != "pending" {
		return fmt.Errorf("service: RejectReturn: request is already %q", rr.Status)
	}
	if err := s.orderRepo.ResolveReturn(requestID, actorID, false); err != nil {
		return err
	}
	observability.Orders.Info("return rejected", "request_id", requestID, "actor_id", actorID)
	return nil
}

// ---------------------------------------------------------------
// Read-through helpers exposed to handlers
// ---------------------------------------------------------------

// GetOrderByID returns the order and its items.
func (s *OrderService) GetOrderByID(orderID int64) (*models.Order, []models.OrderItem, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("service: GetOrderByID: %w", err)
	}
	items, err := s.orderRepo.GetItemsByOrderID(orderID)
	if err != nil {
		return nil, nil, fmt.Errorf("service: GetOrderByID: items: %w", err)
	}
	return order, items, nil
}

// GetOrdersForUser returns paginated orders for a user.
func (s *OrderService) GetOrdersForUser(userID int64, limit, offset int) ([]models.Order, error) {
	return s.orderRepo.GetByUserID(userID, limit, offset)
}

// GetAllOrders returns paginated orders for admin view, with optional filters.
func (s *OrderService) GetAllOrders(status, dateFrom, dateTo string, limit, offset int) ([]models.Order, error) {
	return s.orderRepo.GetAll(status, dateFrom, dateTo, limit, offset)
}

// GetOrderEvents returns the event timeline for an order.
func (s *OrderService) GetOrderEvents(orderID int64) ([]models.OrderEvent, error) {
	return s.orderRepo.GetEventsByOrderID(orderID)
}

// GetReturnRequestsForUser returns all return requests by a user.
func (s *OrderService) GetReturnRequestsForUser(userID int64) ([]models.ReturnRequest, error) {
	return s.orderRepo.GetReturnRequestsByUser(userID)
}

// GetPendingReturnRequests returns all pending return requests (admin view).
func (s *OrderService) GetPendingReturnRequests(limit, offset int) ([]models.ReturnRequest, error) {
	return s.orderRepo.GetReturnRequests("pending", limit, offset)
}
