package services

import (
	"database/sql"
	"errors"
	"fmt"

	"w2t86/internal/models"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
)

// IssueItem identifies a single material line to be physically issued.
// IssuedQty is the number of copies the clerk is handing out right now; it may
// be less than the full ordered quantity, in which case a backorder is created
// for the remainder.  When IssuedQty is 0 it defaults to the full ordered qty.
type IssueItem struct {
	MaterialID int64
	Qty        int // total ordered quantity
	IssuedQty  int // quantity being issued in this operation (0 = full qty)
}

// DistributionService orchestrates all clerk-facing distribution operations:
// issuing, returning, exchanging, and reissuing physical copies.
type DistributionService struct {
	distRepo     *repository.DistributionRepository
	orderRepo    *repository.OrderRepository
	materialRepo *repository.MaterialRepository
}

// NewDistributionService wires the service to its three repositories.
func NewDistributionService(
	dr *repository.DistributionRepository,
	or *repository.OrderRepository,
	mr *repository.MaterialRepository,
) *DistributionService {
	return &DistributionService{
		distRepo:     dr,
		orderRepo:    or,
		materialRepo: mr,
	}
}

// ---------------------------------------------------------------
// Issue
// ---------------------------------------------------------------

// IssueItems records an "issued" distribution event for every item in the
// slice, marks the corresponding order_items as fulfilled, and — when all
// items for the order are fulfilled — advances the order status to in_transit.
//
// scanID is a barcode / copy identifier that binds the physical copy to the
// event.  It is shared across all items in a single issue batch.
func (s *DistributionService) IssueItems(orderID, actorID int64, scanID string, items []IssueItem) error {
	if len(items) == 0 {
		return errors.New("service: IssueItems: at least one item required")
	}

	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("service: IssueItems: order %d not found", orderID)
		}
		return fmt.Errorf("service: IssueItems: load order: %w", err)
	}
	if order.Status != "pending_shipment" && order.Status != "in_transit" {
		return fmt.Errorf("service: IssueItems: order %d is not ready to issue (status=%s)", orderID, order.Status)
	}

	scanIDPtr := &scanID
	actorIDPtr := &actorID
	orderIDPtr := &orderID

	for _, item := range items {
		if item.Qty <= 0 {
			return fmt.Errorf("service: IssueItems: qty must be positive for material %d", item.MaterialID)
		}
		issued := item.IssuedQty
		if issued <= 0 {
			issued = item.Qty
		}
		if issued > item.Qty {
			return fmt.Errorf("service: IssueItems: issued_qty (%d) cannot exceed ordered qty (%d) for material %d",
				issued, item.Qty, item.MaterialID)
		}

		// Verify the material exists.
		if _, err := s.materialRepo.GetByID(item.MaterialID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("service: IssueItems: material %d not found", item.MaterialID)
			}
			return fmt.Errorf("service: IssueItems: load material %d: %w", item.MaterialID, err)
		}

		evt := &models.DistributionEvent{
			OrderID:     orderIDPtr,
			MaterialID:  item.MaterialID,
			Qty:         issued, // record only the quantity actually handed out
			EventType:   "issued",
			ScanID:      scanIDPtr,
			ActorID:     actorIDPtr,
			CustodyFrom: stringPtr("clerk"),
			CustodyTo:   stringPtr("student"),
		}
		if _, err := s.distRepo.RecordEvent(evt); err != nil {
			return fmt.Errorf("service: IssueItems: record event for material %d: %w", item.MaterialID, err)
		}
	}

	// Mark order items as fulfilled and potentially advance order status.
	if err := s.markItemsFulfilled(orderID, actorID, items); err != nil {
		return fmt.Errorf("service: IssueItems: mark fulfilled: %w", err)
	}

	for _, item := range items {
		observability.Distribution.Info("item issued", "order_id", orderID, "material_id", item.MaterialID, "qty", item.Qty, "scan_id", scanID, "actor_id", actorID)
	}
	return nil
}

// markItemsFulfilled updates fulfillment_status for each issued item.
// When the issued quantity equals the ordered quantity the item is marked
// "fulfilled"; when it is less, the item is marked "backordered" and a
// backorder record is created for the shortfall.
// The order is advanced from pending_shipment → in_transit on the first issue.
func (s *DistributionService) markItemsFulfilled(orderID, actorID int64, items []IssueItem) error {
	for _, item := range items {
		issued := item.IssuedQty
		if issued <= 0 {
			issued = item.Qty
		}

		if issued >= item.Qty {
			// Fully satisfied — mark fulfilled.
			if err := s.orderRepo.MarkOrderItemFulfilled(orderID, item.MaterialID); err != nil {
				return fmt.Errorf("markItemsFulfilled: mark fulfilled material %d: %w", item.MaterialID, err)
			}
		} else {
			// Partial issue — mark backordered and record the shortfall.
			if err := s.orderRepo.MarkOrderItemBackordered(orderID, item.MaterialID); err != nil {
				return fmt.Errorf("markItemsFulfilled: mark backordered material %d: %w", item.MaterialID, err)
			}
			itemID, err := s.orderRepo.GetOrderItemID(orderID, item.MaterialID)
			if err != nil {
				return fmt.Errorf("markItemsFulfilled: get order_item_id material %d: %w", item.MaterialID, err)
			}
			shortfall := item.Qty - issued
			if _, err := s.orderRepo.CreateBackorder(itemID, shortfall); err != nil {
				return fmt.Errorf("markItemsFulfilled: create backorder material %d: %w", item.MaterialID, err)
			}
			observability.Distribution.Warn("partial issue — backorder created",
				"order_id", orderID, "material_id", item.MaterialID,
				"issued", issued, "shortfall", shortfall)
		}
	}

	// Advance order from pending_shipment → in_transit on first issue.
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return err
	}
	if order.Status == "pending_shipment" {
		if err := s.orderRepo.Transition(orderID, actorID, "in_transit", "items issued by clerk", s.materialRepo); err != nil {
			return fmt.Errorf("markItemsFulfilled: advance to in_transit: %w", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------
// Return
// ---------------------------------------------------------------

// RecordReturn records a "returned" distribution event and releases inventory
// (increments available_qty by qty).
func (s *DistributionService) RecordReturn(orderID, materialID, actorID int64, scanID string, qty int) error {
	if qty <= 0 {
		return errors.New("service: RecordReturn: qty must be positive")
	}

	// Verify order exists.
	if _, err := s.orderRepo.GetByID(orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("service: RecordReturn: order %d not found", orderID)
		}
		return fmt.Errorf("service: RecordReturn: load order: %w", err)
	}

	orderIDPtr := &orderID
	actorIDPtr := &actorID
	scanIDPtr := &scanID

	evt := &models.DistributionEvent{
		OrderID:     orderIDPtr,
		MaterialID:  materialID,
		Qty:         qty,
		EventType:   "returned",
		ScanID:      scanIDPtr,
		ActorID:     actorIDPtr,
		CustodyFrom: stringPtr("student"),
		CustodyTo:   stringPtr("clerk"),
	}
	if _, err := s.distRepo.RecordEvent(evt); err != nil {
		return fmt.Errorf("service: RecordReturn: record event: %w", err)
	}

	// Release inventory back to available stock.
	if err := s.materialRepo.Release(materialID, qty); err != nil {
		return fmt.Errorf("service: RecordReturn: release inventory: %w", err)
	}

	observability.Distribution.Info("item returned", "order_id", orderID, "material_id", materialID, "qty", qty, "scan_id", scanID, "actor_id", actorID)
	return nil
}

// ---------------------------------------------------------------
// Exchange
// ---------------------------------------------------------------

// RecordExchange swaps an old copy for a new one in a single logical operation:
//  1. Records a "returned" event for oldMaterialID.
//  2. Verifies the new material has available stock.
//  3. Records an "issued" event for newMaterialID and decrements its
//     available_qty.
func (s *DistributionService) RecordExchange(orderID, oldMaterialID, newMaterialID, actorID int64, scanID string, qty int) error {
	if qty <= 0 {
		return errors.New("service: RecordExchange: qty must be positive")
	}

	// Return the old copy first (releases its inventory).
	if err := s.RecordReturn(orderID, oldMaterialID, actorID, scanID, qty); err != nil {
		return fmt.Errorf("service: RecordExchange: return old: %w", err)
	}

	// Check new material stock.
	newMat, err := s.materialRepo.GetByID(newMaterialID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("service: RecordExchange: new material %d not found", newMaterialID)
		}
		return fmt.Errorf("service: RecordExchange: load new material: %w", err)
	}
	if newMat.AvailableQty < qty {
		return fmt.Errorf("service: RecordExchange: insufficient stock for material %q (available %d, requested %d)",
			newMat.Title, newMat.AvailableQty, qty)
	}

	orderIDPtr := &orderID
	actorIDPtr := &actorID
	newScanID := scanID + "_exch"
	scanIDPtr := &newScanID

	evt := &models.DistributionEvent{
		OrderID:     orderIDPtr,
		MaterialID:  newMaterialID,
		Qty:         qty,
		EventType:   "issued",
		ScanID:      scanIDPtr,
		ActorID:     actorIDPtr,
		CustodyFrom: stringPtr("clerk"),
		CustodyTo:   stringPtr("student"),
	}
	if _, err := s.distRepo.RecordEvent(evt); err != nil {
		return fmt.Errorf("service: RecordExchange: record issue event: %w", err)
	}

	// Reserve the newly issued material's inventory.
	if err := s.materialRepo.Reserve(newMaterialID, qty); err != nil {
		return fmt.Errorf("service: RecordExchange: reserve new material inventory: %w", err)
	}

	observability.Distribution.Info("item exchanged", "order_id", orderID, "old_material_id", oldMaterialID, "new_material_id", newMaterialID, "qty", qty, "actor_id", actorID)
	return nil
}


// ---------------------------------------------------------------
// Reissue
// ---------------------------------------------------------------

// ReissueItem handles lost or damaged copy replacement:
//   - Records a "lost" or "damaged" event for oldScanID (marking it out of
//     circulation).
//   - Records a new "issued" event for newScanID.
//
// reason must be "lost" or "damaged".
func (s *DistributionService) ReissueItem(orderID, materialID, actorID int64, oldScanID, newScanID, reason string) error {
	if reason != "lost" && reason != "damaged" {
		return fmt.Errorf("service: ReissueItem: invalid reason %q (must be 'lost' or 'damaged')", reason)
	}
	if oldScanID == "" || newScanID == "" {
		return errors.New("service: ReissueItem: both oldScanID and newScanID are required")
	}

	// Verify order exists.
	if _, err := s.orderRepo.GetByID(orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("service: ReissueItem: order %d not found", orderID)
		}
		return fmt.Errorf("service: ReissueItem: load order: %w", err)
	}

	orderIDPtr := &orderID
	actorIDPtr := &actorID

	// Record the loss/damage event for the old scan ID.
	oldEvt := &models.DistributionEvent{
		OrderID:    orderIDPtr,
		MaterialID: materialID,
		Qty:        1,
		EventType:  reason, // "lost" or "damaged"
		ScanID:     &oldScanID,
		ActorID:    actorIDPtr,
	}
	if _, err := s.distRepo.RecordEvent(oldEvt); err != nil {
		return fmt.Errorf("service: ReissueItem: record %s event: %w", reason, err)
	}

	// Issue the replacement copy.
	newEvt := &models.DistributionEvent{
		OrderID:     orderIDPtr,
		MaterialID:  materialID,
		Qty:         1,
		EventType:   "issued",
		ScanID:      &newScanID,
		ActorID:     actorIDPtr,
		CustodyFrom: stringPtr("clerk"),
		CustodyTo:   stringPtr("student"),
	}
	if _, err := s.distRepo.RecordEvent(newEvt); err != nil {
		return fmt.Errorf("service: ReissueItem: record replacement issue event: %w", err)
	}

	observability.Distribution.Info("item reissued", "old_scan_id", oldScanID, "new_scan_id", newScanID, "reason", reason)
	return nil
}

// ---------------------------------------------------------------
// Ledger / chain queries — thin pass-through
// ---------------------------------------------------------------

// GetLedger returns a paginated, filtered view of distribution events.
func (s *DistributionService) GetLedger(filters repository.DistributionFilter, limit, offset int) ([]models.DistributionEvent, error) {
	return s.distRepo.ListEvents(filters, limit, offset)
}

// GetCustodyChain returns the full event history for a physical copy.
func (s *DistributionService) GetCustodyChain(scanID string) ([]models.DistributionEvent, error) {
	if scanID == "" {
		return nil, errors.New("service: GetCustodyChain: scanID is required")
	}
	return s.distRepo.GetCustodyChain(scanID)
}

// GetPendingIssues returns the clerk's pick list.
func (s *DistributionService) GetPendingIssues(limit, offset int) ([]repository.PendingIssue, error) {
	return s.distRepo.GetPendingIssues(limit, offset)
}

// CountBackorders returns the total number of unresolved backorder records.
func (s *DistributionService) CountBackorders() (int, error) {
	return s.distRepo.CountBackorders()
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

func stringPtr(s string) *string { return &s }
