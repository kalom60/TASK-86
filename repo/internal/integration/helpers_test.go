package integration_test

// Integration tests for the w2t86 portal.
//
// Template rendering notes:
//   - The Fiber app is built WITH the html template engine pointing to
//     ../../web/templates (relative to this package).  Most handlers call
//     c.Render, so we need the engine wired up.
//   - For handlers that are templated but also have HTMX paths that return
//     plain text/JSON, we send HX-Request: true to get text responses.
//   - For handlers whose success path is a redirect (302/303), we test just
//     the redirect status code — no template is rendered.
//   - For handlers that render a full-page template on success, tests assert
//     exactly http.StatusOK (200). A 500 response always indicates a real bug
//     and must never be treated as acceptable.
//
// IMPORTANT: Fiber v2 with multiple app.Group("", mw...) shares middleware
// across ALL requests because the empty prefix makes the group middleware global.
// To avoid cross-group RBAC interference, we use per-route middleware by
// passing the RBAC handler directly as an additional argument to each route
// registration instead of using groups with empty prefix for RBAC.

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"

	"w2t86/internal/config"
	"w2t86/internal/handlers"
	"w2t86/internal/middleware"
	"w2t86/internal/models"
	"w2t86/internal/repository"
	"w2t86/internal/services"
	"w2t86/internal/testutil"
)

// ---------------------------------------------------------------------------
// TestApp — builds a complete wired Fiber app backed by an in-memory SQLite DB.
// ---------------------------------------------------------------------------

// newTestApp builds the full Fiber app wired exactly like main.go but using an
// in-memory test DB and a template engine pointing to ../../web/templates.
// It returns the app, the underlying DB, and a cleanup function.
//
// RBAC is applied per-route (not via empty-prefix groups) to avoid Fiber v2's
// behavior of running ALL group middlewares for ALL requests when the group
// prefix is "".
func newTestApp(t *testing.T) (*fiber.App, *sql.DB, func()) {
	t.Helper()

	db := testutil.NewTestDB(t)

	// Wire repositories.
	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	materialRepo := repository.NewMaterialRepository(db)
	engagementRepo := repository.NewEngagementRepository(db)
	orderRepo := repository.NewOrderRepository(db)
	distributionRepo := repository.NewDistributionRepository(db)
	messagingRepo := repository.NewMessagingRepository(db)
	moderationRepo := repository.NewModerationRepository(db)
	analyticsRepo := repository.NewAnalyticsRepository(db)
	adminRepo := repository.NewAdminRepository(db)
	courseRepo := repository.NewCourseRepository(db)

	// Wire config (minimal).
	cfg := &config.Config{
		AppEnv:        "test",
		SessionSecret: "test-secret",
		EncryptionKey: "",
	}

	// Wire services.
	authService := services.NewAuthService(userRepo, sessionRepo, cfg)
	materialService := services.NewMaterialService(materialRepo, engagementRepo)
	orderService := services.NewOrderService(orderRepo, materialRepo)
	courseService := services.NewCourseService(courseRepo, materialRepo)
	distributionService := services.NewDistributionService(distributionRepo, orderRepo, materialRepo)
	messagingService := services.NewMessagingService(messagingRepo)
	moderationService := services.NewModerationService(moderationRepo)
	analyticsService := services.NewAnalyticsService(analyticsRepo)
	adminService := services.NewAdminService(adminRepo, userRepo, materialRepo)

	// Wire handlers.
	authHandler := handlers.NewAuthHandler(authService)
	materialHandler := handlers.NewMaterialHandler(materialService)
	orderHandler := handlers.NewOrderHandler(orderService)
	courseHandler := handlers.NewCourseHandler(courseService)
	distributionHandler := handlers.NewDistributionHandler(distributionService)
	messagingHandler := handlers.NewMessagingHandler(messagingService)
	moderationHandler := handlers.NewModerationHandler(moderationService, messagingService)
	analyticsHandler := handlers.NewAnalyticsHandler(analyticsService)
	adminHandler := handlers.NewAdminHandler(adminService, authService)

	// Wire middleware.
	authMiddleware := middleware.NewAuthMiddleware(sessionRepo, userRepo)
	requireAuth := authMiddleware.RequireAuth()

	// Per-role RBAC middleware helpers.
	requireClerkOrAdmin := middleware.RequireRole("clerk", "admin")
	requireModOrAdmin := middleware.RequireRole("moderator", "admin")
	requireInstrOrAdmin := middleware.RequireRole("instructor", "admin")
	requireAdmin := middleware.RequireRole("admin")

	loginLimiter := middleware.NewRateLimiter(10, time.Minute)
	loginRateLimit := loginLimiter.Middleware(func(c *fiber.Ctx) string { return c.IP() })
	commentRateLimit := middleware.CommentRateLimit()

	// Template engine — point at the real templates directory relative to
	// this package (internal/integration → ../../web/templates).
	// Register custom template functions that are used in the templates.
	engine := html.New("../../web/templates", ".html")
	// Templates use custom math functions and type conversions.
	engine.AddFunc("mul", func(a, b float64) float64 { return a * b })
	engine.AddFunc("add", func(a, b int) int { return a + b })
	engine.AddFunc("sub", func(a, b float64) float64 { return a - b })
	engine.AddFunc("div", func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	})
	engine.AddFunc("float64", func(v interface{}) float64 {
		switch n := v.(type) {
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case float64:
			return n
		}
		return 0
	})
	engine.AddFunc("dict", func(values ...interface{}) (map[string]interface{}, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict requires even number of arguments")
		}
		dict := make(map[string]interface{}, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings")
			}
			dict[key] = values[i+1]
		}
		return dict, nil
	})
	engine.AddFunc("dec", func(n int) int { return n - 1 })
	engine.AddFunc("inc", func(n int) int { return n + 1 })
	engine.AddFunc("deref", func(v interface{}) interface{} {
		if v == nil {
			return nil
		}
		return v
	})
	engine.AddFunc("hourRange", func() []int {
		hours := make([]int, 24)
		for i := range hours {
			hours[i] = i
		}
		return hours
	})

	app := fiber.New(fiber.Config{
		Views:       engine,
		ViewsLayout: "layouts/main",
		// Don't return the default 500-page HTML; return plain text errors so
		// test assertions on status codes are clean.
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).SendString(err.Error())
		},
	})

	// Inject enc_key so admin handlers don't panic.
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("enc_key", cfg.EncryptionKey)
		return c.Next()
	})

	// Health.
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// ------------------------------------------------------------------
	// Public routes (no auth required)
	// ------------------------------------------------------------------
	app.Get("/login", authHandler.LoginPage)
	app.Post("/login", loginRateLimit, authHandler.Login)
	app.Get("/share/:token", materialHandler.SharedList)

	// ------------------------------------------------------------------
	// Protected routes — RequireAuth only
	// ------------------------------------------------------------------
	app.Post("/logout", requireAuth, authHandler.Logout)

	app.Get("/dashboard", requireAuth, func(c *fiber.Ctx) error {
		user := middleware.GetUser(c)
		switch user.Role {
		case "admin":
			return c.Redirect("/dashboard/admin", fiber.StatusFound)
		case "instructor":
			return c.Redirect("/dashboard/instructor", fiber.StatusFound)
		default:
			return c.Render("dashboard", fiber.Map{
				"Title": "Dashboard",
				"User":  user,
			}, "layouts/base")
		}
	})

	// Browse history
	app.Get("/history", requireAuth, materialHandler.BrowseHistory)

	// Materials catalog
	app.Get("/materials", requireAuth, materialHandler.ListPage)
	app.Get("/materials/search", requireAuth, materialHandler.SearchPartial)
	app.Get("/materials/:id", requireAuth, materialHandler.DetailPage)

	app.Post("/materials/:id/rate", requireAuth, materialHandler.Rate)
	app.Post("/materials/:id/comments", requireAuth, commentRateLimit, materialHandler.AddComment)
	app.Post("/comments/:id/report", requireAuth, materialHandler.ReportComment)

	// Favorites
	app.Get("/favorites", requireAuth, materialHandler.FavoritesList)
	app.Post("/favorites", requireAuth, materialHandler.CreateFavoritesList)
	app.Get("/favorites/:id", requireAuth, materialHandler.GetFavoritesListDetail)
	app.Get("/favorites/:id/items", requireAuth, materialHandler.GetFavoritesListItems)
	app.Post("/favorites/:id/items", requireAuth, materialHandler.AddToFavorites)
	app.Delete("/favorites/:id/items/:materialID", requireAuth, materialHandler.RemoveFromFavorites)
	app.Get("/favorites/:id/share", requireAuth, materialHandler.ShareFavoritesList)

	// Orders (student)
	app.Get("/orders", requireAuth, orderHandler.ListOrders)
	app.Get("/orders/cart", requireAuth, orderHandler.CartPage)
	app.Get("/orders/:id", requireAuth, orderHandler.OrderDetail)

	app.Post("/orders", requireAuth, orderHandler.PlaceOrder)
	app.Post("/orders/:id/pay", requireAuth, orderHandler.ConfirmPayment)
	app.Post("/orders/:id/cancel", requireAuth, orderHandler.CancelOrder)
	app.Post("/orders/:id/returns", requireAuth, orderHandler.SubmitReturnRequest)

	// Returns (student view)
	app.Get("/returns", requireAuth, orderHandler.ListReturnRequests)

	// Inbox / messaging
	app.Get("/inbox", requireAuth, messagingHandler.Inbox)
	app.Get("/inbox/items", requireAuth, messagingHandler.InboxItems)
	app.Post("/inbox/:id/read", requireAuth, messagingHandler.MarkRead)
	app.Post("/inbox/read-all", requireAuth, messagingHandler.MarkAllRead)
	app.Get("/inbox/settings", requireAuth, messagingHandler.Settings)
	app.Post("/inbox/settings/dnd", requireAuth, messagingHandler.UpdateDND)
	app.Post("/inbox/subscribe", requireAuth, messagingHandler.Subscribe)
	app.Post("/inbox/unsubscribe", requireAuth, messagingHandler.Unsubscribe)
	app.Get("/inbox/badge", requireAuth, messagingHandler.Badge)

	// ------------------------------------------------------------------
	// Clerk / Admin routes
	// ------------------------------------------------------------------
	app.Get("/distribution", requireAuth, requireClerkOrAdmin, distributionHandler.PickList)
	app.Post("/distribution/issue", requireAuth, requireClerkOrAdmin, distributionHandler.IssueItems)
	app.Post("/distribution/return", requireAuth, requireClerkOrAdmin, distributionHandler.RecordReturn)
	app.Post("/distribution/exchange", requireAuth, requireClerkOrAdmin, distributionHandler.RecordExchange)
	app.Post("/distribution/reissue", requireAuth, requireClerkOrAdmin, distributionHandler.ReissueItem)
	app.Get("/distribution/ledger", requireAuth, requireClerkOrAdmin, distributionHandler.Ledger)
	app.Get("/distribution/ledger/search", requireAuth, requireClerkOrAdmin, distributionHandler.LedgerSearch)
	app.Get("/distribution/custody/:scanID", requireAuth, requireClerkOrAdmin, distributionHandler.CustodyChain)
	app.Get("/admin/orders", requireAuth, requireClerkOrAdmin, orderHandler.AdminListOrders)
	app.Post("/admin/orders/:id/ship", requireAuth, requireClerkOrAdmin, orderHandler.MarkShipped)
	app.Post("/admin/orders/:id/deliver", requireAuth, requireClerkOrAdmin, orderHandler.MarkDelivered)

	// ------------------------------------------------------------------
	// Moderator routes
	// ------------------------------------------------------------------
	app.Get("/moderation", requireAuth, requireModOrAdmin, moderationHandler.Queue)
	app.Get("/moderation/items", requireAuth, requireModOrAdmin, moderationHandler.QueueItems)
	app.Post("/moderation/:id/approve", requireAuth, requireModOrAdmin, moderationHandler.Approve)
	app.Post("/moderation/:id/remove", requireAuth, requireModOrAdmin, moderationHandler.Remove)

	// ------------------------------------------------------------------
	// Instructor / Admin routes
	// ------------------------------------------------------------------
	app.Get("/dashboard/instructor", requireAuth, requireInstrOrAdmin, analyticsHandler.InstructorDashboard)
	app.Get("/admin/returns", requireAuth, requireInstrOrAdmin, orderHandler.AdminListReturnRequests)
	app.Post("/admin/returns/:id/approve", requireAuth, requireInstrOrAdmin, orderHandler.ApproveReturn)
	app.Post("/admin/returns/:id/reject", requireAuth, requireInstrOrAdmin, orderHandler.RejectReturn)
	app.Post("/admin/orders/:id/cancel", requireAuth, requireInstrOrAdmin, orderHandler.AdminCancelOrder)

	// Course planning
	app.Get("/courses", requireAuth, requireInstrOrAdmin, courseHandler.ListCourses)
	app.Get("/courses/new", requireAuth, requireInstrOrAdmin, courseHandler.NewCourseForm)
	app.Post("/courses", requireAuth, requireInstrOrAdmin, courseHandler.CreateCourse)
	app.Get("/courses/:id", requireAuth, requireInstrOrAdmin, courseHandler.CourseDetail)
	app.Post("/courses/:id/plan", requireAuth, requireInstrOrAdmin, courseHandler.AddPlanItem)
	app.Post("/courses/:id/plan/:planID/approve", requireAuth, requireInstrOrAdmin, courseHandler.ApprovePlanItem)

	// ------------------------------------------------------------------
	// Admin-only routes
	// ------------------------------------------------------------------
	app.Get("/dashboard/admin", requireAuth, requireAdmin, analyticsHandler.AdminDashboard)

	app.Get("/admin/materials/new", requireAuth, requireAdmin, materialHandler.NewMaterialForm)
	app.Post("/admin/materials", requireAuth, requireAdmin, materialHandler.CreateMaterial)
	app.Get("/admin/materials/:id/edit", requireAuth, requireAdmin, materialHandler.EditMaterialForm)
	app.Put("/admin/materials/:id", requireAuth, requireAdmin, materialHandler.UpdateMaterial)
	app.Delete("/admin/materials/:id", requireAuth, requireAdmin, materialHandler.DeleteMaterial)

	app.Get("/admin/users", requireAuth, requireAdmin, adminHandler.ListUsers)
	app.Get("/admin/users/new", requireAuth, requireAdmin, adminHandler.NewUserForm)
	app.Post("/admin/users", requireAuth, requireAdmin, adminHandler.CreateUser)
	app.Get("/admin/users/:id", requireAuth, requireAdmin, adminHandler.UserProfile)
	app.Post("/admin/users/:id/role", requireAuth, requireAdmin, adminHandler.UpdateRole)
	app.Post("/admin/users/:id/unlock", requireAuth, requireAdmin, adminHandler.UnlockUser)
	app.Get("/admin/users/:id/fields", requireAuth, requireAdmin, adminHandler.CustomFieldsPage)
	app.Post("/admin/users/:id/fields", requireAuth, requireAdmin, adminHandler.SetCustomField)
	app.Delete("/admin/users/:id/fields/:name", requireAuth, requireAdmin, adminHandler.DeleteCustomField)

	app.Get("/admin/duplicates", requireAuth, requireAdmin, adminHandler.DuplicatesPage)
	app.Post("/admin/duplicates/merge", requireAuth, requireAdmin, adminHandler.MergeUsers)

	app.Get("/admin/audit", requireAuth, requireAdmin, adminHandler.AuditLogPage)
	app.Get("/admin/audit/:entityType/:entityID", requireAuth, requireAdmin, adminHandler.EntityAuditLog)

	app.Get("/analytics/map", requireAuth, requireAdmin, analyticsHandler.MapPage)
	app.Get("/analytics/map/data", requireAuth, requireAdmin, analyticsHandler.MapData)
	app.Post("/analytics/map/compute", requireAuth, requireAdmin, analyticsHandler.ComputeGrid)
	app.Get("/analytics/export/orders", requireAuth, requireAdmin, analyticsHandler.ExportOrders)
	app.Get("/analytics/export/distribution", requireAuth, requireAdmin, analyticsHandler.ExportDistribution)
	app.Get("/analytics/kpi/:name", requireAuth, requireAdmin, analyticsHandler.KPIHistory)

	cleanup := func() {
		// DB is closed by t.Cleanup registered in NewTestDB.
	}

	return app, db, cleanup
}

// ---------------------------------------------------------------------------
// loginAs — creates a user with the given role, logs in, returns cookie string.
// ---------------------------------------------------------------------------

// loginAs creates a user with the given role, sends POST /login, and returns the
// "session_token=<value>" cookie string suitable for use in subsequent requests.
// The returned string can be passed directly as the Cookie header value.
func loginAs(t *testing.T, app *fiber.App, db *sql.DB, role string) string {
	t.Helper()

	// Create a unique username per call so parallel tests don't collide.
	username := fmt.Sprintf("testuser_%s_%d", role, time.Now().UnixNano())
	password := "TestPassword123!"
	email := username + "@example.com"

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	cfg := &config.Config{SessionSecret: "test-secret"}
	authSvc := services.NewAuthService(userRepo, sessionRepo, cfg)

	if _, err := authSvc.Register(username, email, password, role); err != nil {
		t.Fatalf("loginAs: register user: %v", err)
	}

	body := fmt.Sprintf("username=%s&password=%s", username, password)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("loginAs: app.Test: %v", err)
	}
	defer resp.Body.Close()

	// Login success redirects to /dashboard (302).
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("loginAs: unexpected status %d; body: %s", resp.StatusCode, string(b))
	}

	// Extract the Set-Cookie header.
	for _, c := range resp.Cookies() {
		if c.Name == "session_token" {
			return "session_token=" + c.Value
		}
	}
	t.Fatal("loginAs: no session_token cookie found in response")
	return ""
}

// ---------------------------------------------------------------------------
// makeRequest — fires a request against the test Fiber app.
// ---------------------------------------------------------------------------

// makeRequest builds a request and calls app.Test.
// - method: HTTP verb ("GET", "POST", etc.)
// - path: request path ("/orders/1")
// - body: raw request body (empty string for no body)
// - cookie: cookie header value (e.g. "session_token=abc123"); empty for none
// - contentType: e.g. "application/x-www-form-urlencoded"; empty defaults to no Content-Type
func makeRequest(app *fiber.App, method, path, body, cookie, contentType string, extraHeaders ...map[string]string) *http.Response {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, h := range extraHeaders {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		// Return a synthetic 500 response on internal test errors.
		return &http.Response{StatusCode: http.StatusInternalServerError}
	}
	return resp
}

// htmxHeaders returns a map with HX-Request: true for use with makeRequest's
// extraHeaders parameter.
func htmxHeaders() map[string]string {
	return map[string]string{"HX-Request": "true"}
}

// ---------------------------------------------------------------------------
// createTestMaterial — inserts a test material directly into the DB.
// ---------------------------------------------------------------------------

// createTestMaterial inserts an active material row directly into the DB and
// returns the populated model.
func createTestMaterial(t *testing.T, db *sql.DB) *models.Material {
	t.Helper()
	repo := repository.NewMaterialRepository(db)
	title := fmt.Sprintf("Test Book %d", time.Now().UnixNano())
	m := &models.Material{
		Title:        title,
		TotalQty:     10,
		AvailableQty: 10,
		ReservedQty:  0,
		Status:       "active",
	}
	created, err := repo.Create(m)
	if err != nil {
		t.Fatalf("createTestMaterial: %v", err)
	}
	return created
}

// ---------------------------------------------------------------------------
// createTestOrder — places a test order for the given userID.
// ---------------------------------------------------------------------------

// createTestOrder creates an order for the given userID. It first creates a
// material with stock, then places the order via the repository directly.
func createTestOrder(t *testing.T, db *sql.DB, userID int64) *models.Order {
	t.Helper()
	mat := createTestMaterial(t, db)
	orderRepo := repository.NewOrderRepository(db)
	order, err := orderRepo.Create(userID, []repository.OrderItemInput{
		{MaterialID: mat.ID, Qty: 1, UnitPrice: 9.99},
	})
	if err != nil {
		t.Fatalf("createTestOrder: %v", err)
	}
	return order
}

// ---------------------------------------------------------------------------
// createTestUser — creates a user directly in the DB and returns it.
// ---------------------------------------------------------------------------

func createTestUser(t *testing.T, db *sql.DB, role string) *models.User {
	t.Helper()
	username := fmt.Sprintf("user_%s_%d", role, time.Now().UnixNano())
	email := username + "@example.com"
	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	cfg := &config.Config{SessionSecret: "test-secret"}
	authSvc := services.NewAuthService(userRepo, sessionRepo, cfg)
	user, err := authSvc.Register(username, email, "TestPassword123!", role)
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
	return user
}

// ---------------------------------------------------------------------------
// readBody — helper to read response body as string.
// ---------------------------------------------------------------------------

func readBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
