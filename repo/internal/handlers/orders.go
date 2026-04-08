package handlers

import (
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
	"w2t86/internal/services"
)

// OrderHandler handles all order-lifecycle HTTP routes for both student and
// admin/clerk users.
type OrderHandler struct {
	orderService *services.OrderService
}

// NewOrderHandler creates an OrderHandler backed by the given service.
func NewOrderHandler(os *services.OrderService) *OrderHandler {
	return &OrderHandler{orderService: os}
}

// ---------------------------------------------------------------
// Student routes
// ---------------------------------------------------------------

// ListOrders handles GET /orders — renders the authenticated user's order list.
func (h *OrderHandler) ListOrders(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 20
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	orders, err := h.orderService.GetOrdersForUser(user.ID, limit, offset)
	if err != nil {
		return internalErr(c, observability.Orders, "get orders for user failed", err, "user_id", user.ID)
	}

	return c.Render("orders/list", fiber.Map{
		"Title":      "My Orders",
		"User":       user,
		"Orders":     orders,
		"ActivePage": "orders",
	}, "layouts/base")
}

// OrderDetail handles GET /orders/:id — renders the full order detail page.
func (h *OrderHandler) OrderDetail(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)

	order, items, err := h.orderService.GetOrderByID(int64(id))
	if err != nil {
		return apiErr(c, fiber.StatusNotFound, "Order not found")
	}

	// Students may only view their own orders.
	if user.Role == "student" && order.UserID != user.ID {
		observability.Security.Warn("unauthorized order access attempt",
			"user_id", user.ID, "order_id", id, "order_owner_id", order.UserID, "ip", c.IP())
		return apiErr(c, fiber.StatusForbidden, "You do not have permission to perform this action")
	}

	events, err := h.orderService.GetOrderEvents(int64(id))
	if err != nil {
		observability.Orders.Warn("get order events failed", "order_id", id, "error", err)
		events = nil
	}

	// Enrich items with backorder status — an item is backordered when
	// fulfillment_status is "backordered".
	return c.Render("orders/detail", fiber.Map{
		"Title":      fmt.Sprintf("Order #%d", id),
		"User":       user,
		"Order":      order,
		"Items":      items,
		"Events":     events,
		"ActivePage": "orders",
	}, "layouts/base")
}

// CartPage handles GET /orders/cart — renders the cart/checkout page.
// Items are expected as query parameters: material_id[]=1&qty[]=2 etc.
func (h *OrderHandler) CartPage(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	return c.Render("orders/cart", fiber.Map{
		"Title":      "Checkout",
		"User":       user,
		"ActivePage": "orders",
	}, "layouts/base")
}

// PlaceOrder handles POST /orders — places an order from the submitted cart form.
func (h *OrderHandler) PlaceOrder(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	// Parse multi-value form fields: material_id[] and qty[] and unit_price[].
	materialIDs := c.Request().PostArgs().PeekMulti("material_id")
	qtys := c.Request().PostArgs().PeekMulti("qty")
	prices := c.Request().PostArgs().PeekMulti("unit_price")

	if len(materialIDs) == 0 || len(materialIDs) != len(qtys) {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid cart data")
	}

	items := make([]repository.OrderItemInput, 0, len(materialIDs))
	for i, rawID := range materialIDs {
		mid, err := strconv.ParseInt(string(rawID), 10, 64)
		if err != nil {
			return htmxErr(c, fiber.StatusBadRequest, "Invalid material ID in cart")
		}
		qty, err := strconv.Atoi(string(qtys[i]))
		if err != nil || qty <= 0 {
			return htmxErr(c, fiber.StatusBadRequest, "Invalid quantity in cart")
		}
		var unitPrice float64
		if i < len(prices) {
			unitPrice, _ = strconv.ParseFloat(string(prices[i]), 64)
		}
		items = append(items, repository.OrderItemInput{
			MaterialID: mid,
			Qty:        qty,
			UnitPrice:  unitPrice,
		})
	}

	order, err := h.orderService.PlaceOrder(user.ID, items)
	if err != nil {
		observability.Orders.Warn("place order rejected", "user_id", user.ID, "error", err)
		errData := fiber.Map{
			"Title": "Checkout",
			"User":  user,
			"Error": "Could not place your order. Please check your cart and try again.",
		}
		if c.Get("HX-Request") == "true" {
			return c.Status(fiber.StatusUnprocessableEntity).Render("orders/cart", errData)
		}
		return c.Status(fiber.StatusUnprocessableEntity).Render("orders/cart", errData, "layouts/base")
	}

	observability.Orders.Info("order placed by user", "order_id", order.ID, "user_id", user.ID)
	return c.Redirect(fmt.Sprintf("/orders/%d", order.ID), fiber.StatusFound)
}

// ConfirmPayment handles POST /orders/:id/pay — confirms payment via HTMX.
func (h *OrderHandler) ConfirmPayment(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.ConfirmPayment(int64(id), user.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not confirm payment. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		// Return updated status badge partial.
		return c.Render("partials/order_status_badge", fiber.Map{
			"Status": "pending_shipment",
		})
	}
	return c.Redirect(fmt.Sprintf("/orders/%d", id), fiber.StatusFound)
}

// CancelOrder handles POST /orders/:id/cancel — cancels an order.
func (h *OrderHandler) CancelOrder(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.CancelOrder(int64(id), user.ID, user.Role); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not cancel order. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/order_status_badge", fiber.Map{
			"Status": "canceled",
		})
	}
	return c.Redirect(fmt.Sprintf("/orders/%d", id), fiber.StatusFound)
}

// ---------------------------------------------------------------
// Clerk / Admin routes
// ---------------------------------------------------------------

// AdminCancelOrder handles POST /admin/orders/:id/cancel.
// Instructors and admins may cancel orders that are in pending_shipment or
// in_transit (admins only) status.
func (h *OrderHandler) AdminCancelOrder(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.CancelOrder(int64(id), user.ID, user.Role); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not cancel order. Please try again.")
	}

	observability.Orders.Info("order canceled by staff", "order_id", id, "actor_id", user.ID, "role", user.Role)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/order_status_badge", fiber.Map{
			"Status": "canceled",
		})
	}
	return c.Redirect(fmt.Sprintf("/orders/%d", id), fiber.StatusFound)
}

// AdminListOrders handles GET /admin/orders — all orders with optional filters.
func (h *OrderHandler) AdminListOrders(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	status := c.Query("status")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	limit := 50
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	orders, err := h.orderService.GetAllOrders(status, dateFrom, dateTo, limit, offset)
	if err != nil {
		return internalErr(c, observability.Orders, "get all orders failed", err, "status", status)
	}

	return c.Render("admin/orders/list", fiber.Map{
		"Title":        "All Orders",
		"User":         user,
		"Orders":       orders,
		"StatusFilter": status,
		"DateFrom":     dateFrom,
		"DateTo":       dateTo,
		"ActivePage":   "orders",
	}, "layouts/base")
}

// MarkShipped handles POST /admin/orders/:id/ship.
func (h *OrderHandler) MarkShipped(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.MarkShipped(int64(id), user.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not mark order as shipped. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/order_status_badge", fiber.Map{
			"Status": "in_transit",
		})
	}
	return c.Redirect(fmt.Sprintf("/admin/orders/%d", id), fiber.StatusFound)
}

// MarkDelivered handles POST /admin/orders/:id/deliver.
func (h *OrderHandler) MarkDelivered(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.MarkDelivered(int64(id), user.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not mark order as delivered. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/order_status_badge", fiber.Map{
			"Status": "completed",
		})
	}
	return c.Redirect(fmt.Sprintf("/admin/orders/%d", id), fiber.StatusFound)
}

// ---------------------------------------------------------------
// Returns routes
// ---------------------------------------------------------------

// SubmitReturnRequest handles POST /orders/:id/returns.
func (h *OrderHandler) SubmitReturnRequest(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid order ID")
	}

	user := middleware.GetUser(c)
	reqType := c.FormValue("type")
	reason := c.FormValue("reason")

	var replacementID *int64
	if raw := c.FormValue("replacement_material_id"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			replacementID = &n
		}
	}

	rr, err := h.orderService.RequestReturn(int64(id), user.ID, reqType, reason, replacementID)
	if err != nil {
		observability.Orders.Warn("return request rejected", "order_id", id, "user_id", user.ID, "type", reqType, "error", err)
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not submit return request. Please check your input and try again.")
	}

	observability.Orders.Info("return request submitted", "request_id", rr.ID, "order_id", id, "user_id", user.ID, "type", reqType)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": fmt.Sprintf("Return request #%d submitted.", rr.ID),
		})
	}
	return c.Redirect("/returns", fiber.StatusFound)
}

// ListReturnRequests handles GET /returns — user's own return requests.
func (h *OrderHandler) ListReturnRequests(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	requests, err := h.orderService.GetReturnRequestsForUser(user.ID)
	if err != nil {
		observability.Orders.Warn("get return requests for user failed", "user_id", user.ID, "error", err)
		requests = nil
	}

	return c.Render("returns/list", fiber.Map{
		"Title":    "My Returns",
		"User":     user,
		"Requests": requests,
	}, "layouts/base")
}

// AdminListReturnRequests handles GET /admin/returns — pending returns queue.
func (h *OrderHandler) AdminListReturnRequests(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	limit := 50
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	requests, err := h.orderService.GetPendingReturnRequests(limit, offset)
	if err != nil {
		observability.Orders.Warn("get pending return requests failed", "error", err)
		requests = nil
	}

	return c.Render("admin/returns/list", fiber.Map{
		"Title":    "Return Requests",
		"User":     user,
		"Requests": requests,
	}, "layouts/base")
}

// ApproveReturn handles POST /admin/returns/:id/approve.
func (h *OrderHandler) ApproveReturn(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid return request ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.ApproveReturn(int64(id), user.ID, user.Role); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not approve return request. Please try again.")
	}

	observability.Orders.Info("return request approved", "request_id", id, "actor_id", user.ID, "role", user.Role)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": fmt.Sprintf("Return request #%d approved.", id),
		})
	}
	return c.Redirect("/admin/returns", fiber.StatusFound)
}

// RejectReturn handles POST /admin/returns/:id/reject.
func (h *OrderHandler) RejectReturn(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid return request ID")
	}

	user := middleware.GetUser(c)
	if err := h.orderService.RejectReturn(int64(id), user.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not reject return request. Please try again.")
	}

	observability.Orders.Info("return request rejected", "request_id", id, "actor_id", user.ID)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": fmt.Sprintf("Return request #%d rejected.", id),
		})
	}
	return c.Redirect("/admin/returns", fiber.StatusFound)
}
