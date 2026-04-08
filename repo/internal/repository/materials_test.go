package repository_test

import (
	"testing"

	"w2t86/internal/models"
	"w2t86/internal/repository"
	"w2t86/internal/testutil"
)

func newMaterialRepo(t *testing.T) *repository.MaterialRepository {
	t.Helper()
	db := testutil.NewTestDB(t)
	return repository.NewMaterialRepository(db)
}

func makeMaterial(title string, qty int) *models.Material {
	return &models.Material{
		Title:        title,
		TotalQty:     qty,
		AvailableQty: qty,
		ReservedQty:  0,
		Status:       "active",
	}
}

func TestMaterialRepository_Create_And_GetByID(t *testing.T) {
	repo := newMaterialRepo(t)

	m, err := repo.Create(makeMaterial("Go Programming", 10))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := repo.GetByID(m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != "Go Programming" {
		t.Errorf("title mismatch: got %q", got.Title)
	}
}

func TestMaterialRepository_Search_FTS5(t *testing.T) {
	db := testutil.NewTestDB(t)
	repo := repository.NewMaterialRepository(db)

	// Skip if FTS5 virtual table was stripped (not compiled in).
	var ftsCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='materials_fts'`).Scan(&ftsCount); err != nil {
		t.Fatalf("probe fts5: %v", err)
	}
	if ftsCount == 0 {
		t.Skip("FTS5 not available in this SQLite build — skipping search test")
	}

	if _, err := repo.Create(makeMaterial("Introduction to Algebra", 5)); err != nil {
		t.Fatalf("Create algebra: %v", err)
	}
	if _, err := repo.Create(makeMaterial("Advanced Calculus", 3)); err != nil {
		t.Fatalf("Create calculus: %v", err)
	}

	results, err := repo.Search("Algebra", 10, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'Algebra', got %d", len(results))
	}
	if results[0].Title != "Introduction to Algebra" {
		t.Errorf("unexpected result: %q", results[0].Title)
	}
}

func TestMaterialRepository_Reserve_Success(t *testing.T) {
	repo := newMaterialRepo(t)

	m, err := repo.Create(makeMaterial("Biology", 10))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Reserve(m.ID, 3); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	got, err := repo.GetByID(m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AvailableQty != 7 {
		t.Errorf("expected available_qty=7, got %d", got.AvailableQty)
	}
	if got.ReservedQty != 3 {
		t.Errorf("expected reserved_qty=3, got %d", got.ReservedQty)
	}
}

func TestMaterialRepository_Reserve_InsufficientStock_Fails(t *testing.T) {
	repo := newMaterialRepo(t)

	m, err := repo.Create(makeMaterial("Chemistry", 2))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = repo.Reserve(m.ID, 5)
	if err == nil {
		t.Error("expected error for insufficient stock, got nil")
	}
}

func TestMaterialRepository_Release_RestoresQty(t *testing.T) {
	repo := newMaterialRepo(t)

	m, err := repo.Create(makeMaterial("Physics", 10))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Reserve(m.ID, 4); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := repo.Release(m.ID, 2); err != nil {
		t.Fatalf("Release: %v", err)
	}

	got, err := repo.GetByID(m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AvailableQty != 8 {
		t.Errorf("expected available_qty=8, got %d", got.AvailableQty)
	}
	if got.ReservedQty != 2 {
		t.Errorf("expected reserved_qty=2, got %d", got.ReservedQty)
	}
}

func TestMaterialRepository_Fulfill_DecrementsReserved(t *testing.T) {
	repo := newMaterialRepo(t)

	m, err := repo.Create(makeMaterial("History", 10))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Reserve(m.ID, 5); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := repo.Fulfill(m.ID, 3); err != nil {
		t.Fatalf("Fulfill: %v", err)
	}

	got, err := repo.GetByID(m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ReservedQty != 2 {
		t.Errorf("expected reserved_qty=2, got %d", got.ReservedQty)
	}
}

func TestMaterialRepository_SoftDelete_HidesFromList(t *testing.T) {
	repo := newMaterialRepo(t)

	m, err := repo.Create(makeMaterial("Deleted Book", 1))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SoftDelete(m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	_, err = repo.GetByID(m.ID)
	if err == nil {
		t.Error("expected error for soft-deleted material, got nil")
	}

	list, err := repo.List(100, 0, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, mat := range list {
		if mat.ID == m.ID {
			t.Error("soft-deleted material appeared in List results")
		}
	}
}
