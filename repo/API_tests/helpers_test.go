package api_tests

// API functional tests for the w2t86 portal.
//
// These tests exercise the HTTP API layer end-to-end through a wired Fiber app
// backed by an in-memory SQLite database.  They cover three broad scenarios:
//
//  1. Normal inputs   — happy path: valid credentials, well-formed payloads.
//  2. Missing/invalid parameters — bad inputs that should return 4xx errors.
//  3. Permission errors — unauthenticated or wrong-role access returns 401/403.

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
// newTestApp builds a full Fiber app backed by an in-memory test DB.
// ---------------------------------------------------------------------------

func newTestApp(t *testing.T) (*fiber.App, *sql.DB, func()) {
	t.Helper()

	db := testutil.NewTestDB(t)

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

	cfg := &config.Config{
		AppEnv:        "test",
		SessionSecret: "test-secret",
		EncryptionKey: "",
	}

	authService := services.NewAuthService(userRepo, sessionRepo, cfg)
	materialService := services.NewMaterialService(materialRepo, engagementRepo)
	orderService := services.NewOrderService(orderRepo, materialRepo)
	distributionService := services.NewDistributionService(distributionRepo, orderRepo, materialRepo)
	messagingService := services.NewMessagingService(messagingRepo)
	moderationService := services.NewModerationService(moderationRepo)
	analyticsService := services.NewAnalyticsService(analyticsRepo)
	adminService := services.NewAdminService(adminRepo, userRepo, materialRepo)

	authHandler := handlers.NewAuthHandler(authService)
	materialHandler := handlers.NewMaterialHandler(materialService)
	orderHandler := handlers.NewOrderHandler(orderService)
	distributionHandler := handlers.NewDistributionHandler(distributionService)
	messagingHandler := handlers.NewMessagingHandler(messagingService)
	moderationHandler := handlers.NewModerationHandler(moderationService, messagingService)
	analyticsHandler := handlers.NewAnalyticsHandler(analyticsService)
	adminHandler := handlers.NewAdminHandler(adminService, authService)

	// Templates live one level above this package directory (API_tests/).
	engine := html.New("../web/templates", ".html")
	engine.Reload(true)
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
		d := make(map[string]interface{}, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings")
			}
			d[key] = values[i+1]
		}
		return d, nil
	})
	engine.AddFunc("dec", func(n int) int { return n - 1 })
	engine.AddFunc("inc", func(n int) int { return n + 1 })
	engine.AddFunc("deref", func(v interface{}) interface{} { return v })
	engine.AddFunc("hourRange", func() []int {
		hours := make([]int, 24)
		for i := range hours {
			hours[i] = i
		}
		return hours
	})

	app := fiber.New(fiber.Config{
		Views:                 engine,
		ViewsLayout:           "layouts/main",
		DisableStartupMessage: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if fe, ok := err.(*fiber.Error); ok {
				code = fe.Code
			}
			return c.Status(code).SendString(err.Error())
		},
	})

	authMW := middleware.NewAuthMiddleware(sessionRepo, userRepo)
	requireAuth := authMW.RequireAuth()
	requireAdmin := middleware.RequireRole("admin")
	requireMod := middleware.RequireRole("moderator", "admin")
	requireClerk := middleware.RequireRole("clerk", "admin")
	requireInstr := middleware.RequireRole("instructor", "admin")
	commentRL := middleware.CommentRateLimit()

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Auth.
	app.Get("/login", authHandler.LoginPage)
	app.Post("/login", authHandler.Login)
	app.Post("/logout", requireAuth, authHandler.Logout)
	app.Get("/dashboard", requireAuth, func(c *fiber.Ctx) error {
		user := middleware.GetUser(c)
		switch user.Role {
		case "admin":
			return c.Redirect("/dashboard/admin", fiber.StatusFound)
		case "instructor":
			return c.Redirect("/dashboard/instructor", fiber.StatusFound)
		default:
			return c.Redirect("/materials", fiber.StatusFound)
		}
	})

	// Materials.
	app.Get("/materials", requireAuth, materialHandler.ListPage)
	app.Get("/materials/search", requireAuth, materialHandler.SearchPartial)
	app.Get("/materials/:id", requireAuth, materialHandler.DetailPage)
	app.Post("/materials/:id/rate", requireAuth, materialHandler.Rate)
	app.Post("/materials/:id/comments", requireAuth, commentRL, materialHandler.AddComment)
	app.Post("/comments/:id/report", requireAuth, materialHandler.ReportComment)

	// Favorites.
	app.Get("/favorites", requireAuth, materialHandler.FavoritesList)
	app.Post("/favorites", requireAuth, materialHandler.CreateFavoritesList)
	app.Post("/favorites/:id/items", requireAuth, materialHandler.AddToFavorites)
	app.Delete("/favorites/:id/items/:materialID", requireAuth, materialHandler.RemoveFromFavorites)
	app.Get("/favorites/:id/share", requireAuth, materialHandler.ShareFavoritesList)
	app.Get("/share/:token", materialHandler.SharedList)

	// Orders.
	app.Get("/orders", requireAuth, orderHandler.ListOrders)
	app.Get("/orders/cart", requireAuth, orderHandler.CartPage)
	app.Get("/orders/:id", requireAuth, orderHandler.OrderDetail)
	app.Post("/orders", requireAuth, orderHandler.PlaceOrder)
	app.Post("/orders/:id/pay", requireAuth, orderHandler.ConfirmPayment)
	app.Post("/orders/:id/cancel", requireAuth, orderHandler.CancelOrder)
	app.Post("/orders/:id/returns", requireAuth, orderHandler.SubmitReturnRequest)
	app.Get("/returns", requireAuth, orderHandler.ListReturnRequests)

	// Messaging.
	app.Get("/inbox", requireAuth, messagingHandler.Inbox)
	app.Get("/inbox/items", requireAuth, messagingHandler.InboxItems)
	app.Post("/inbox/:id/read", requireAuth, messagingHandler.MarkRead)
	app.Post("/inbox/read-all", requireAuth, messagingHandler.MarkAllRead)
	app.Get("/inbox/settings", requireAuth, messagingHandler.Settings)
	app.Post("/inbox/settings/dnd", requireAuth, messagingHandler.UpdateDND)
	app.Post("/inbox/subscribe", requireAuth, messagingHandler.Subscribe)
	app.Post("/inbox/unsubscribe", requireAuth, messagingHandler.Unsubscribe)
	app.Get("/inbox/badge", requireAuth, messagingHandler.Badge)

	// Moderation.
	app.Get("/moderation", requireAuth, requireMod, moderationHandler.Queue)
	app.Get("/moderation/items", requireAuth, requireMod, moderationHandler.QueueItems)
	app.Post("/moderation/:id/approve", requireAuth, requireMod, moderationHandler.Approve)
	app.Post("/moderation/:id/remove", requireAuth, requireMod, moderationHandler.Remove)

	// Distribution.
	app.Get("/distribution", requireAuth, requireClerk, distributionHandler.PickList)
	app.Post("/distribution/issue", requireAuth, requireClerk, distributionHandler.IssueItems)
	app.Post("/distribution/return", requireAuth, requireClerk, distributionHandler.RecordReturn)
	app.Post("/distribution/exchange", requireAuth, requireClerk, distributionHandler.RecordExchange)
	app.Post("/distribution/reissue", requireAuth, requireClerk, distributionHandler.ReissueItem)
	app.Get("/distribution/ledger", requireAuth, requireClerk, distributionHandler.Ledger)
	app.Get("/distribution/ledger/search", requireAuth, requireClerk, distributionHandler.LedgerSearch)
	app.Get("/distribution/custody/:scanID", requireAuth, requireClerk, distributionHandler.CustodyChain)

	// Admin / instructor.
	app.Get("/dashboard/admin", requireAuth, requireAdmin, analyticsHandler.AdminDashboard)
	app.Get("/dashboard/instructor", requireAuth, requireInstr, analyticsHandler.InstructorDashboard)
	app.Get("/admin/orders", requireAuth, requireClerk, orderHandler.AdminListOrders)
	app.Post("/admin/orders/:id/ship", requireAuth, requireClerk, orderHandler.MarkShipped)
	app.Post("/admin/orders/:id/deliver", requireAuth, requireClerk, orderHandler.MarkDelivered)
	app.Get("/admin/returns", requireAuth, requireInstr, orderHandler.AdminListReturnRequests)
	app.Post("/admin/returns/:id/approve", requireAuth, requireInstr, orderHandler.ApproveReturn)
	app.Post("/admin/returns/:id/reject", requireAuth, requireInstr, orderHandler.RejectReturn)
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
	app.Get("/analytics/export/orders", requireAuth, requireAdmin, analyticsHandler.ExportOrders)
	app.Get("/analytics/export/distribution", requireAuth, requireAdmin, analyticsHandler.ExportDistribution)

	return app, db, func() {}
}

// ---------------------------------------------------------------------------
// loginAs creates a user with the given role and returns the session cookie.
// ---------------------------------------------------------------------------

func loginAs(t *testing.T, app *fiber.App, db *sql.DB, role string) string {
	t.Helper()
	username := fmt.Sprintf("api_%s_%d", role, time.Now().UnixNano())
	password := "TestPassword123!"

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	cfg := &config.Config{SessionSecret: "test-secret"}
	svc := services.NewAuthService(userRepo, sessionRepo, cfg)

	if _, err := svc.Register(username, username+"@x.com", password, role); err != nil {
		t.Fatalf("loginAs register: %v", err)
	}

	body := fmt.Sprintf("username=%s&password=%s", username, password)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("loginAs app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("loginAs unexpected status %d: %s", resp.StatusCode, b)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "session_token" {
			return "session_token=" + c.Value
		}
	}
	t.Fatal("loginAs: no session_token cookie")
	return ""
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func makeRequest(app *fiber.App, method, path, body, cookie, ct string, extra ...map[string]string) *http.Response {
	var br io.Reader
	if body != "" {
		br = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, br)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for _, h := range extra {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		return &http.Response{StatusCode: http.StatusInternalServerError}
	}
	return resp
}

func htmx() map[string]string { return map[string]string{"HX-Request": "true"} }

func readBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func createMaterial(t *testing.T, db *sql.DB) *models.Material {
	t.Helper()
	repo := repository.NewMaterialRepository(db)
	m := &models.Material{
		Title:        fmt.Sprintf("API Book %d", time.Now().UnixNano()),
		TotalQty:     10,
		AvailableQty: 10,
		Status:       "active",
	}
	created, err := repo.Create(m)
	if err != nil {
		t.Fatalf("createMaterial: %v", err)
	}
	return created
}

func createUser(t *testing.T, db *sql.DB, role string) *models.User {
	t.Helper()
	username := fmt.Sprintf("cu_%s_%d", role, time.Now().UnixNano())
	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	cfg := &config.Config{SessionSecret: "test-secret"}
	svc := services.NewAuthService(userRepo, sessionRepo, cfg)
	user, err := svc.Register(username, username+"@x.com", "TestPassword123!", role)
	if err != nil {
		t.Fatalf("createUser: %v", err)
	}
	return user
}
