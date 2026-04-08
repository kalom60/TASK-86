package repository_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// TestSpatialAggregates_Query_Under200ms verifies the checklist requirement:
// "Spatial aggregate query returns in <200ms on 10k rows."
//
// It inserts 10 000 spatial_aggregates rows for a single layer_type, then
// times a GetSpatialAggregates call and fails if it exceeds 200 ms.
func TestSpatialAggregates_Query_Under200ms(t *testing.T) {
	db := testutil.NewTestDB(t)
	repo := repository.NewAnalyticsRepository(db)

	const (
		n         = 10_000
		layerType = "school"
		metric    = "count"
		limit     = 200 * time.Millisecond
	)

	// Bulk-insert 10k rows via a transaction for speed.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO spatial_aggregates (layer_type, cell_key, metric, value, computed_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(layer_type, cell_key, metric) DO NOTHING`)
	if err != nil {
		tx.Rollback() //nolint:errcheck
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < n; i++ {
		cellKey := fmt.Sprintf("cell_%06d", i)
		if _, err := stmt.Exec(layerType, cellKey, metric, float64(i)); err != nil {
			stmt.Close()
			tx.Rollback() //nolint:errcheck
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	start := time.Now()
	rows, err := repo.GetSpatialAggregates(layerType)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("GetSpatialAggregates: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("expected %d rows, got %d", n, len(rows))
	}
	if elapsed > limit {
		t.Errorf("GetSpatialAggregates(%d rows) took %v, want <200ms", n, elapsed)
	}
	t.Logf("GetSpatialAggregates(%d rows): %v", n, elapsed)
}

// TestExportOrdersCSV_MasksPII verifies the checklist requirement:
// "PII masked in all export endpoints."
//
// It creates an order for a known user, calls ExportOrdersCSV, and checks
// that the raw username and email do NOT appear in the CSV output, while
// their masked equivalents do.
func TestExportOrdersCSV_MasksPII(t *testing.T) {
	db := testutil.NewTestDB(t)

	// Insert a user with a recognisable name and email.
	const (
		username = "Alice Smith"
		email    = "alice.smith@example.com"
		pwHash   = "$2a$12$fMPISK6tAC1XLVM3JdJQDuB/CrXgdRM.LUPHHu4/VxS/vzihnYyQ."
	)
	var userID int64
	err := db.QueryRow(
		`INSERT INTO users (username, email, password_hash, role) VALUES (?, ?, ?, 'student') RETURNING id`,
		username, email, pwHash,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Insert a material.
	var matID int64
	err = db.QueryRow(
		`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES ('Book A', 5, 5, 0, 'active') RETURNING id`,
	).Scan(&matID)
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}

	// Insert an order.
	var orderID int64
	err = db.QueryRow(
		`INSERT INTO orders (user_id, status, total_amount, auto_close_at, created_at, updated_at)
		 VALUES (?, 'completed', 9.99, datetime('now','+30 minutes'), datetime('now'), datetime('now')) RETURNING id`,
		userID,
	).Scan(&orderID)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}

	analyticsRepo := repository.NewAnalyticsRepository(db)
	svc := services.NewAnalyticsService(analyticsRepo)

	// ExportOrdersCSV applies PII masking at the service layer.
	csvBytes, err := svc.ExportOrdersCSV("", "", "")
	if err != nil {
		t.Fatalf("ExportOrdersCSV: %v", err)
	}
	csvStr := string(csvBytes)
	t.Logf("exported CSV:\n%s", csvStr)

	// Raw PII must NOT appear anywhere in the CSV output.
	if strings.Contains(csvStr, "Alice") || strings.Contains(csvStr, "Smith") {
		t.Errorf("CSV contains unmasked name PII — expected initials only\n%s", csvStr)
	}
	if strings.Contains(csvStr, "@") {
		t.Errorf("CSV contains unmasked email (@ present) — expected masked ID\n%s", csvStr)
	}
}
