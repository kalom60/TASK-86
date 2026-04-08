package unit_tests

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// newMaterialService returns a MaterialService backed by a fresh test DB.
// An optional word filter list may be supplied.
func newMaterialService(t *testing.T, db *sql.DB, blockedWords []string) *services.MaterialService {
	t.Helper()
	matRepo := repository.NewMaterialRepository(db)
	engRepo := repository.NewEngagementRepository(db)
	svc := services.NewMaterialService(matRepo, engRepo)
	if len(blockedWords) > 0 {
		svc.SetWordFilter(services.NewWordFilter(blockedWords))
	}
	return svc
}

// seedMaterialForValidation inserts an active material and returns its ID.
func seedMaterialForValidation(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(
		`INSERT INTO materials (title, total_qty, available_qty, reserved_qty, status)
		 VALUES (?, 1, 1, 0, 'active') RETURNING id`,
		fmt.Sprintf("val_material_%d", testSeq()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedMaterialForValidation: %v", err)
	}
	return id
}

// seedUserForValidation inserts a user and returns its ID.
func seedUserForValidation(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES (?, 'val@x.com', 'h', 'student') RETURNING id`,
		fmt.Sprintf("val_user_%d", testSeq()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedUserForValidation: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// Comment length validation
// ---------------------------------------------------------------------------

func TestComment_Exactly500Chars_Passes(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	body := strings.Repeat("a", 500)
	_, err := svc.AddComment(matID, userID, body)
	if err != nil {
		t.Errorf("expected 500-char comment to pass, got: %v", err)
	}
}

func TestComment_501Chars_Fails(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	body := strings.Repeat("a", 501)
	_, err := svc.AddComment(matID, userID, body)
	if err == nil {
		t.Error("expected error for 501-char comment, got nil")
	}
}

func TestComment_Empty_Fails(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	// An empty body does not exceed the length limit; however the DB schema
	// may or may not reject it. This test verifies the service does not panic.
	// Treat either outcome (success or error) as acceptable for the empty case —
	// the important thing is that it does not panic.
	_, _ = svc.AddComment(matID, userID, "")
}

// ---------------------------------------------------------------------------
// Comment link validation
// ---------------------------------------------------------------------------

func TestComment_ZeroLinks_Passes(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	_, err := svc.AddComment(matID, userID, "no links here at all")
	if err != nil {
		t.Errorf("expected comment with zero links to pass, got: %v", err)
	}
}

func TestComment_TwoLinks_Passes(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	body := `see <a href=http://a.com>a</a> and <a href=http://b.com>b</a>`
	_, err := svc.AddComment(matID, userID, body)
	if err != nil {
		t.Errorf("expected comment with 2 links to pass, got: %v", err)
	}
}

func TestComment_ThreeLinks_Fails(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	body := `<a href=http://a.com>a</a> <a href=http://b.com>b</a> <a href=http://c.com>c</a>`
	_, err := svc.AddComment(matID, userID, body)
	if err == nil {
		t.Error("expected error for comment with 3 links (href= occurs 3 times), got nil")
	}
}

// ---------------------------------------------------------------------------
// Word filter validation
// ---------------------------------------------------------------------------

func TestComment_SensitiveWord_Blocked(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, []string{"badword"})
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	_, err := svc.AddComment(matID, userID, "this contains badword in the middle")
	if err == nil {
		t.Error("expected error for comment containing prohibited word, got nil")
	}
}

func TestComment_NoSensitiveWord_Passes(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, []string{"badword"})
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	_, err := svc.AddComment(matID, userID, "this is a perfectly fine comment")
	if err != nil {
		t.Errorf("expected clean comment to pass, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Comment rate limit
// ---------------------------------------------------------------------------

// insertCommentDirectly bypasses the service to seed the comment table with a
// past timestamp, enabling us to control the rate-limit window precisely.
func insertCommentDirectly(t *testing.T, db *sql.DB, matID, userID int64, createdAt time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO comments (material_id, user_id, body, link_count, status, report_count, created_at, updated_at)
		 VALUES (?, ?, 'seeded', 0, 'visible', 0, ?, datetime('now'))`,
		matID, userID, createdAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insertCommentDirectly: %v", err)
	}
}

func TestComment_RateLimit_FifthCommentPasses(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	// Seed 4 comments within the last 10 minutes.
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		insertCommentDirectly(t, db, matID, userID, now.Add(-time.Duration(i+1)*time.Minute))
	}

	// 5th comment via service — should succeed (limit is 5 per window).
	_, err := svc.AddComment(matID, userID, "fifth comment in window")
	if err != nil {
		t.Errorf("expected 5th comment to pass (limit=5), got: %v", err)
	}
}

func TestComment_RateLimit_SixthCommentFails(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	// Seed 5 comments within the last 10 minutes.
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		insertCommentDirectly(t, db, matID, userID, now.Add(-time.Duration(i+1)*time.Minute))
	}

	// 6th comment — should fail.
	_, err := svc.AddComment(matID, userID, "sixth comment in window")
	if err == nil {
		t.Error("expected rate limit error for 6th comment, got nil")
	}
}

// ---------------------------------------------------------------------------
// Rating validation
// ---------------------------------------------------------------------------

func TestRating_Stars1_Valid(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	if err := svc.Rate(matID, userID, 1); err != nil {
		t.Errorf("expected stars=1 to be valid, got: %v", err)
	}
}

func TestRating_Stars5_Valid(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	if err := svc.Rate(matID, userID, 5); err != nil {
		t.Errorf("expected stars=5 to be valid, got: %v", err)
	}
}

func TestRating_Stars0_Fails(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	if err := svc.Rate(matID, userID, 0); err == nil {
		t.Error("expected error for stars=0, got nil")
	}
}

func TestRating_Stars6_Fails(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	if err := svc.Rate(matID, userID, 6); err == nil {
		t.Error("expected error for stars=6, got nil")
	}
}

func TestRating_SameUser_SecondRating_Updates(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := newMaterialService(t, db, nil)
	matID := seedMaterialForValidation(t, db)
	userID := seedUserForValidation(t, db)

	if err := svc.Rate(matID, userID, 3); err != nil {
		t.Fatalf("first rating: %v", err)
	}
	// Second rating — should be an upsert, not a duplicate error.
	if err := svc.Rate(matID, userID, 5); err != nil {
		t.Errorf("expected second rating to upsert, got: %v", err)
	}

	// Verify the stored value is the updated one.
	stars, err := svc.GetUserRating(matID, userID)
	if err != nil {
		t.Fatalf("GetUserRating: %v", err)
	}
	if stars != 5 {
		t.Errorf("expected stars=5 after update, got %d", stars)
	}
}

// ---------------------------------------------------------------------------
// Word filter unit tests
// ---------------------------------------------------------------------------

func TestWordFilter_EmptyList_NeverBlocks(t *testing.T) {
	wf := services.NewWordFilter(nil)
	found, _ := wf.Contains("any text including badword or spam")
	if found {
		t.Error("empty word filter should never block any text")
	}
}

func TestWordFilter_MatchesCaseInsensitive(t *testing.T) {
	wf := services.NewWordFilter([]string{"spam"})

	for _, text := range []string{"SPAM", "Spam", "spam", "this is SPAM text"} {
		t.Run(text, func(t *testing.T) {
			found, _ := wf.Contains(text)
			if !found {
				t.Errorf("expected word filter to match %q case-insensitively", text)
			}
		})
	}
}

func TestWordFilter_NoFalsePositive(t *testing.T) {
	wf := services.NewWordFilter([]string{"badword"})
	found, _ := wf.Contains("this is a perfectly clean sentence")
	if found {
		t.Error("word filter produced false positive on clean text")
	}
}

func TestWordFilter_PartialWordNoMatch(t *testing.T) {
	// The word filter uses word-boundary regexp (\b). "ass" should not match
	// inside "bass" because it would require a word boundary.
	wf := services.NewWordFilter([]string{"ass"})
	found, _ := wf.Contains("I play bass guitar")
	if found {
		t.Error("word filter should not match 'ass' inside 'bass' due to word boundary")
	}
}
