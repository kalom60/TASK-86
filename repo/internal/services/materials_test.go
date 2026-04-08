package services_test

import (
	"database/sql"
	"strings"
	"testing"

	"w2t86/internal/models"
	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

func newMaterialService(t *testing.T) (*services.MaterialService, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	matRepo := repository.NewMaterialRepository(db)
	engRepo := repository.NewEngagementRepository(db)
	svc := services.NewMaterialService(matRepo, engRepo)
	return svc, db
}

func insertMatUser(t *testing.T, db *sql.DB, username string) int64 {
	t.Helper()
	r, err := db.Exec(`INSERT INTO users (username, email, password_hash, role) VALUES (?,?,'hash','student')`,
		username, username+"@x.com")
	if err != nil {
		t.Fatalf("insertMatUser %q: %v", username, err)
	}
	id, _ := r.LastInsertId()
	return id
}

func insertTestMaterial(t *testing.T, db *sql.DB, title string) int64 {
	t.Helper()
	r, err := db.Exec(`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status) VALUES (?,10,10,0,'active')`, title)
	if err != nil {
		t.Fatalf("insertTestMaterial %q: %v", title, err)
	}
	id, _ := r.LastInsertId()
	return id
}

// ---------------------------------------------------------------
// WordFilter tests
// ---------------------------------------------------------------

func TestWordFilter_Contains_MatchesCaseInsensitive(t *testing.T) {
	wf := services.NewWordFilter([]string{"spam", "badword"})

	t.Run("exact match", func(t *testing.T) {
		found, word := wf.Contains("this is spam")
		if !found {
			t.Error("expected to find 'spam'")
		}
		if word != "spam" {
			t.Errorf("expected word=spam, got %q", word)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		found, _ := wf.Contains("This Is SPAM okay")
		if !found {
			t.Error("expected case-insensitive match for SPAM")
		}
	})

	t.Run("second word", func(t *testing.T) {
		found, word := wf.Contains("a badword here")
		if !found {
			t.Error("expected to find 'badword'")
		}
		if word != "badword" {
			t.Errorf("expected word=badword, got %q", word)
		}
	})
}

func TestWordFilter_NotContains(t *testing.T) {
	wf := services.NewWordFilter([]string{"spam", "badword"})

	found, word := wf.Contains("this is perfectly fine content")
	if found {
		t.Errorf("expected no match, got %q", word)
	}
}

// ---------------------------------------------------------------
// AddComment tests
// ---------------------------------------------------------------

func TestMaterialService_AddComment_Success(t *testing.T) {
	svc, db := newMaterialService(t)
	userID := insertMatUser(t, db, "commenter1")
	matID := insertTestMaterial(t, db, "Test Book")

	c, err := svc.AddComment(matID, userID, "This is a great book!")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("expected non-zero comment ID")
	}
	if c.Body != "This is a great book!" {
		t.Errorf("unexpected body: %q", c.Body)
	}
}

func TestMaterialService_AddComment_TooLong_Fails(t *testing.T) {
	svc, db := newMaterialService(t)
	userID := insertMatUser(t, db, "commenter2")
	matID := insertTestMaterial(t, db, "Test Book 2")

	// 501 characters
	longBody := strings.Repeat("a", 501)
	_, err := svc.AddComment(matID, userID, longBody)
	if err == nil {
		t.Error("expected error for comment body exceeding 500 chars, got nil")
	}
}

func TestMaterialService_AddComment_TooManyLinks_Fails(t *testing.T) {
	svc, db := newMaterialService(t)
	userID := insertMatUser(t, db, "commenter3")
	matID := insertTestMaterial(t, db, "Test Book 3")

	// 3 href= occurrences — exceeds limit of 2
	body := `check <a href="a">link1</a> and <a href="b">link2</a> and <a href="c">link3</a>`
	_, err := svc.AddComment(matID, userID, body)
	if err == nil {
		t.Error("expected error for too many links, got nil")
	}
}

func TestMaterialService_AddComment_SensitiveWord_Fails(t *testing.T) {
	svc, db := newMaterialService(t)
	userID := insertMatUser(t, db, "commenter4")
	matID := insertTestMaterial(t, db, "Test Book 4")

	wf := services.NewWordFilter([]string{"forbidden"})
	svc.SetWordFilter(wf)

	_, err := svc.AddComment(matID, userID, "this contains forbidden content")
	if err == nil {
		t.Error("expected error for comment with sensitive word, got nil")
	}
}

// ---------------------------------------------------------------
// Rate tests
// ---------------------------------------------------------------

func TestMaterialService_Rate_Success(t *testing.T) {
	svc, db := newMaterialService(t)
	userID := insertMatUser(t, db, "rater1")
	matID := insertTestMaterial(t, db, "Rateable Book")

	if err := svc.Rate(matID, userID, 4); err != nil {
		t.Fatalf("Rate: %v", err)
	}

	stars, err := svc.GetUserRating(matID, userID)
	if err != nil {
		t.Fatalf("GetUserRating: %v", err)
	}
	if stars != 4 {
		t.Errorf("expected 4 stars, got %d", stars)
	}
}

func TestMaterialService_Rate_DuplicateRating_Upserts(t *testing.T) {
	svc, db := newMaterialService(t)
	userID := insertMatUser(t, db, "rater2")
	matID := insertTestMaterial(t, db, "Upsert Book")

	if err := svc.Rate(matID, userID, 2); err != nil {
		t.Fatalf("Rate first: %v", err)
	}
	// Update to 5 stars
	if err := svc.Rate(matID, userID, 5); err != nil {
		t.Fatalf("Rate second: %v", err)
	}

	stars, err := svc.GetUserRating(matID, userID)
	if err != nil {
		t.Fatalf("GetUserRating: %v", err)
	}
	if stars != 5 {
		t.Errorf("expected 5 stars after upsert, got %d", stars)
	}

	// Only one rating row should exist
	_, count, err := svc.GetAverageRating(matID)
	if err != nil {
		t.Fatalf("GetAverageRating: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 rating row after upsert, got %d", count)
	}
}

// Ensure the models import is not flagged as unused when no models.Material is
// directly referenced in a test that compiles.  The insertTestMaterial helper
// above uses raw SQL; we need at least one reference to models to keep the
// import alive.
var _ = (*models.Material)(nil)
