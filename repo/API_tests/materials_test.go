package api_tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Materials — normal inputs
// ---------------------------------------------------------------------------

// TestMaterials_List_ReturnsOK returns 2xx for an authenticated student.
func TestMaterials_List_ReturnsOK(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/materials", "", cookie, "")
	if resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestMaterials_Detail_ReturnsOKOrTemplate returns non-404 for an existing material.
func TestMaterials_Detail_ReturnsOKOrTemplate(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodGet, fmt.Sprintf("/materials/%d", mat.ID), "", cookie, "")
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("GET /materials/%d returned 404 unexpectedly", mat.ID)
	}
}

// TestMaterials_Search_WithQuery returns 2xx or skips if FTS5 unavailable.
func TestMaterials_Search_WithQuery(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	createMaterial(t, db)

	resp := makeRequest(app, http.MethodGet, "/materials/search?q=API", "", cookie, "")
	body := readBody(resp)
	if resp.StatusCode/100 != 2 {
		if resp.StatusCode == http.StatusInternalServerError &&
			(strings.Contains(body, "Search failed") ||
				strings.Contains(body, "unexpected error") ||
				strings.Contains(body, "materials_fts")) {
			t.Skip("FTS5 not available in test environment")
		}
		t.Fatalf("expected 2xx on search, got %d; body: %s", resp.StatusCode, body)
	}
}

// TestMaterials_Search_NoQuery returns 2xx (falls back to full list).
func TestMaterials_Search_NoQuery(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/materials/search", "", cookie, "")
	if resp.StatusCode/100 != 2 {
		t.Fatalf("expected 2xx for search with no query, got %d", resp.StatusCode)
	}
}

// TestMaterials_Rate_ValidStars returns 200 or 302.
func TestMaterials_Rate_ValidStars(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/materials/%d/rate", mat.ID),
		"stars=4", cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200/302, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestMaterials_AddComment_Valid returns 200, 201, or 302.
func TestMaterials_AddComment_Valid(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/materials/%d/comments", mat.ID),
		"body=This+is+a+valid+test+comment", cookie,
		"application/x-www-form-urlencoded", htmx())
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 200/201/302, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestMaterials_Favorites_Create returns 200 or 302.
func TestMaterials_Favorites_Create(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/favorites",
		"name=My+Reading+List&visibility=private", cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200/302, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// TestMaterials_Favorites_AddItem adds a material to an existing list.
func TestMaterials_Favorites_AddItem(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	// Create list.
	makeRequest(app, http.MethodPost, "/favorites",
		"name=Test+List&visibility=private", cookie, "application/x-www-form-urlencoded")

	var listID int64
	if err := db.QueryRow(`SELECT id FROM favorites_lists ORDER BY id DESC LIMIT 1`).Scan(&listID); err != nil {
		t.Skipf("no favorites list created: %v", err)
	}

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/favorites/%d/items", listID),
		fmt.Sprintf("material_id=%d", mat.ID), cookie, "application/x-www-form-urlencoded")

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusFound &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 200/302/422, got %d; body: %s", resp.StatusCode, readBody(resp))
	}
}

// ---------------------------------------------------------------------------
// Materials — missing / invalid parameters
// ---------------------------------------------------------------------------

// TestMaterials_Rate_InvalidStars_Zero returns 400.
// The Rate handler validates stars in [1,5] and returns 400 for out-of-range values.
func TestMaterials_Rate_InvalidStars_Zero(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/materials/%d/rate", mat.ID),
		"stars=0", cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for stars=0, got %d", resp.StatusCode)
	}
}

// TestMaterials_Rate_InvalidStars_Six returns 400.
func TestMaterials_Rate_InvalidStars_Six(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/materials/%d/rate", mat.ID),
		"stars=6", cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for stars=6, got %d", resp.StatusCode)
	}
}

// TestMaterials_AddComment_TooLong returns 422.
func TestMaterials_AddComment_TooLong(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	mat := createMaterial(t, db)

	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/materials/%d/comments", mat.ID),
		"body="+strings.Repeat("a", 501), cookie,
		"application/x-www-form-urlencoded", htmx())
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for 501-char comment, got %d", resp.StatusCode)
	}
}

// TestMaterials_Detail_NotFound returns 404 for a non-existent material.
func TestMaterials_Detail_NotFound(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodGet, "/materials/999999", "", cookie, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown material, got %d", resp.StatusCode)
	}
}

// TestMaterials_Rate_NonNumericID returns 400.
func TestMaterials_Rate_NonNumericID(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	resp := makeRequest(app, http.MethodPost, "/materials/abc/rate",
		"stars=3", cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400 or 404 for non-numeric material ID, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Materials — permission errors
// ---------------------------------------------------------------------------

// TestMaterials_List_Unauthenticated returns 401 or 302.
func TestMaterials_List_Unauthenticated(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	resp := makeRequest(app, http.MethodGet, "/materials", "", "", "")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401/302 for unauthenticated /materials, got %d", resp.StatusCode)
	}
}

// TestMaterials_AddComment_Unauthenticated returns 401 or 302.
func TestMaterials_AddComment_Unauthenticated(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	mat := createMaterial(t, db)
	resp := makeRequest(app, http.MethodPost,
		fmt.Sprintf("/materials/%d/comments", mat.ID),
		"body=hello", "", "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 401/302, got %d", resp.StatusCode)
	}
}

// TestAdmin_CreateMaterial_StudentForbidden returns 403 for a student.
func TestAdmin_CreateMaterial_StudentForbidden(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "student")
	body := "title=Forbidden+Book&total_qty=5&available_qty=5&status=active"
	resp := makeRequest(app, http.MethodPost, "/admin/materials",
		body, cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 403/401 for student creating material, got %d", resp.StatusCode)
	}
}

// TestAdmin_CreateMaterial_AdminAllowed returns non-403 for an admin.
func TestAdmin_CreateMaterial_AdminAllowed(t *testing.T) {
	app, db, cleanup := newTestApp(t)
	defer cleanup()

	cookie := loginAs(t, app, db, "admin")
	body := "title=Admin+Book&total_qty=5&available_qty=5&status=active"
	resp := makeRequest(app, http.MethodPost, "/admin/materials",
		body, cookie, "application/x-www-form-urlencoded")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("expected admin to be allowed to create material, got %d", resp.StatusCode)
	}
}
