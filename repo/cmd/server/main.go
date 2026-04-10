package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	fibercsrf "github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/template/html/v2"
	"github.com/joho/godotenv"

	"w2t86/internal/config"
	"w2t86/internal/crypto"
	"w2t86/internal/db"
	"w2t86/internal/handlers"
	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
	"w2t86/internal/scheduler"
	"w2t86/internal/services"
)

func main() {
	// ------------------------------------------------------------------ //
	// 1. Load configuration from environment / .env                       //
	// ------------------------------------------------------------------ //
	// Load .env if present; ignore the error so the server can still start
	// when environment variables are injected directly (e.g. Docker, CI).
	_ = godotenv.Load()
	cfg := config.Load()

	// Fail fast if required secrets are absent.  Running without these would
	// mean sessions cannot be signed and custom fields cannot be encrypted.
	if err := cfg.Validate(); err != nil {
		log.Fatalf("main: invalid configuration: %v", err)
	}

	// Initialize structured loggers as early as possible.
	observability.Init(cfg.AppEnv)

	// ------------------------------------------------------------------ //
	// 2. Open database (runs migrations automatically)                    //
	// ------------------------------------------------------------------ //
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("main: open db: %v", err)
	}
	defer database.Close()

	// Bootstrap / credential-rotation check.
	//
	// Two trigger conditions require auto-rotation to a random password:
	//   1. The migration placeholder "BOOTSTRAP_PENDING_ROTATION" — set by all
	//      new deployments; the hash is non-functional so login is impossible
	//      until rotation occurs.
	//   2. The legacy known bcrypt hash for "ChangeMe123!" — existing deployments
	//      that upgraded from an older migration still get rotated here.
	//
	// In both cases the temporary password is emitted once to the structured log
	// (level=ERROR so it is hard to miss) and must_change_password is set to 1 so
	// the operator is forced through the reset flow on first login.
	const (
		bootstrapPlaceholder = "BOOTSTRAP_PENDING_ROTATION"
		legacyKnownHash      = "$2a$12$fMPISK6tAC1XLVM3JdJQDuB/CrXgdRM.LUPHHu4/VxS/vzihnYyQ."
		legacyKnownPassword  = "ChangeMe123!"
	)
	var storedHash string
	if err := database.QueryRow(`SELECT password_hash FROM users WHERE username = 'admin' LIMIT 1`).Scan(&storedHash); err == nil {
		needsRotation := false
		switch {
		case storedHash == bootstrapPlaceholder:
			// New deployment — placeholder hash detected; rotate immediately.
			needsRotation = true
		case storedHash == legacyKnownHash && crypto.CheckPassword(storedHash, legacyKnownPassword):
			// Existing deployment with the old known password — rotate now.
			needsRotation = true
		case storedHash == legacyKnownHash:
			observability.App.Error("admin account has the legacy hash but bcrypt check failed — seed data may be corrupt")
		}
		if needsRotation {
			if tmpPass, err := crypto.GenerateRandomPassword(); err == nil {
				if newHash, err := crypto.HashPassword(tmpPass); err == nil {
					if _, err := database.Exec(
						`UPDATE users SET password_hash = ?, must_change_password = 1 WHERE username = 'admin'`,
						newHash,
					); err == nil {
						observability.App.Error(
							"SECURITY: admin bootstrap credential auto-rotated — retrieve the temporary password from this log line and change it after first login",
							"temporary_password", tmpPass,
							"action_required", "change_admin_password_immediately",
						)
					}
				}
			}
		}
	}

	// ------------------------------------------------------------------ //
	// 3. Repositories                                                     //
	// ------------------------------------------------------------------ //
	userRepo         := repository.NewUserRepository(database)
	sessionRepo      := repository.NewSessionRepository(database)
	materialRepo     := repository.NewMaterialRepository(database)
	engagementRepo   := repository.NewEngagementRepository(database)
	orderRepo        := repository.NewOrderRepository(database)
	distributionRepo := repository.NewDistributionRepository(database)
	messagingRepo    := repository.NewMessagingRepository(database)
	// Apply configured timezone so DND windows are evaluated in the operator's
	// preferred timezone rather than always UTC.  TIMEZONE env defaults to "UTC".
	if tz := cfg.Timezone; tz != "" && tz != "UTC" {
		if loc, err := time.LoadLocation(tz); err == nil {
			messagingRepo.SetTimezone(loc)
		} else {
			observability.App.Warn("invalid TIMEZONE config — falling back to UTC", "timezone", tz, "error", err)
		}
	}
	moderationRepo   := repository.NewModerationRepository(database)
	analyticsRepo    := repository.NewAnalyticsRepository(database)
	adminRepo        := repository.NewAdminRepository(database)
	courseRepo       := repository.NewCourseRepository(database)

	// ------------------------------------------------------------------ //
	// 4. Services                                                         //
	// ------------------------------------------------------------------ //
	authService         := services.NewAuthService(userRepo, sessionRepo, cfg)
	materialService     := services.NewMaterialService(materialRepo, engagementRepo)
	courseService       := services.NewCourseService(courseRepo, materialRepo)
	if cfg.BannedWords != "" {
		words := strings.Split(cfg.BannedWords, ",")
		for i, w := range words {
			words[i] = strings.TrimSpace(w)
		}
		materialService.SetWordFilter(services.NewWordFilter(words))
	}
	orderService        := services.NewOrderService(orderRepo, materialRepo)
	distributionService := services.NewDistributionService(distributionRepo, orderRepo, materialRepo)
	messagingService    := services.NewMessagingService(messagingRepo)
	moderationService   := services.NewModerationService(moderationRepo)
	analyticsService    := services.NewAnalyticsService(analyticsRepo)
	adminService        := services.NewAdminService(adminRepo, userRepo, materialRepo)

	// ------------------------------------------------------------------ //
	// 5. Handlers                                                         //
	// ------------------------------------------------------------------ //
	authHandler         := handlers.NewAuthHandler(authService)
	materialHandler     := handlers.NewMaterialHandler(materialService)
	orderHandler        := handlers.NewOrderHandler(orderService)
	courseHandler       := handlers.NewCourseHandler(courseService)
	distributionHandler := handlers.NewDistributionHandler(distributionService)
	messagingHandler    := handlers.NewMessagingHandler(messagingService, cfg.Timezone)
	moderationHandler   := handlers.NewModerationHandler(moderationService, messagingService)
	analyticsHandler    := handlers.NewAnalyticsHandler(analyticsService)
	adminHandler        := handlers.NewAdminHandler(adminService, authService)

	// ------------------------------------------------------------------ //
	// 6. Middleware / rate limiters                                       //
	// ------------------------------------------------------------------ //
	authMiddleware := middleware.NewAuthMiddleware(sessionRepo, userRepo, cfg.SessionSecret)

	// Login rate-limiter: max 10 attempts per minute per IP.
	loginLimiter   := middleware.NewRateLimiter(10, time.Minute)
	loginRateLimit := loginLimiter.Middleware(func(c *fiber.Ctx) string {
		return c.IP()
	})

	// Comment rate-limiter: 5 per 10 minutes per authenticated user.
	commentRateLimit := middleware.CommentRateLimit()

	// ------------------------------------------------------------------ //
	// 7. Scheduler                                                        //
	// ------------------------------------------------------------------ //
	sched := scheduler.NewOrderScheduler(database)
	sched.Start()
	defer sched.Stop()

	// ------------------------------------------------------------------ //
	// 8. Template engine                                                  //
	// ------------------------------------------------------------------ //
	engine := html.New("web/templates", ".html")
	if cfg.AppEnv == "development" {
		engine.Reload(true) // re-parse templates on every request in dev
	}
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
	engine.AddFunc("hourRange", func() []map[string]interface{} {
		hours := make([]map[string]interface{}, 24)
		for i := range hours {
			hours[i] = map[string]interface{}{
				"Val":   i,
				"Label": fmt.Sprintf("%02d:00", i),
			}
		}
		return hours
	})

	// ------------------------------------------------------------------ //
	// 9. Fiber app                                                        //
	// ------------------------------------------------------------------ //
	app := fiber.New(fiber.Config{
		Views:       engine,
		ViewsLayout: "layouts/main",
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
			}
			observability.App.Error("unhandled fiber error", "error", err, "path", c.Path(), "method", c.Method(), "ip", c.IP())
			if c.Get("HX-Request") == "true" {
				return c.Status(code).Render("partials/error_inline", fiber.Map{"Msg": "An unexpected error occurred."})
			}
			return c.Status(code).JSON(struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}{Code: code, Msg: "An unexpected error occurred."})
		},
	})

	// Serve static assets from web/static at /static.
	app.Static("/static", "web/static")

	// Observability middleware — register before all routes.
	app.Use(observability.RequestID())
	app.Use(observability.RequestLogger())

	// Health check — used by Docker HEALTHCHECK and load balancers.
	app.Get("/health", func(c *fiber.Ctx) error {
		if err := database.PingContext(c.Context()); err != nil {
			observability.App.Error("health check DB ping failed", "error", err)
			return c.Status(503).JSON(fiber.Map{"status": "unhealthy"})
		}
		return c.JSON(fiber.Map{
			"status": "ok",
			"uptime": observability.M.Uptime(),
		})
	})

	// Metrics endpoint — admin-only; exposes internal counters.
	// Registered here but the handler itself re-checks the role so that
	// the route ordering (before the session middleware) doesn't matter.
	app.Get("/metrics", authMiddleware.RequireAuth(), middleware.RequireRole("admin"), func(c *fiber.Ctx) error {
		return c.JSON(observability.M.ToJSON())
	})

	// Inject encryption key into every request context so admin handlers
	// can decrypt custom fields.
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("enc_key", cfg.EncryptionKey)
		return c.Next()
	})

	// ------------------------------------------------------------------ //
	// 10. Routes                                                          //
	// ------------------------------------------------------------------ //

	// Root redirect.
	app.Get("/", func(c *fiber.Ctx) error {
		return c.Redirect("/dashboard", fiber.StatusFound)
	})

	// ------------------------------------------------------------------
	// Public routes (no auth required)
	// ------------------------------------------------------------------
	app.Get("/login",  authHandler.LoginPage)
	app.Post("/login", loginRateLimit, authHandler.Login)

	// Public shared favorites list.
	app.Get("/share/:token", materialHandler.SharedList)

	// ------------------------------------------------------------------
	// Per-route RBAC — avoids Fiber v2 empty-prefix group interference
	// where group middleware registered via app.Group("", mw) fires as
	// global USE middleware for ALL requests, breaking cross-role access.
	// ------------------------------------------------------------------
	requireAuth := authMiddleware.RequireAuth()

	// CSRF protection applied inline on every state-mutating authenticated route.
	csrfMiddleware := fibercsrf.New(fibercsrf.Config{
		KeyLookup:      "header:X-Csrf-Token",
		CookieName:     "csrf_token",
		CookieHTTPOnly: false,
		CookieSameSite: "Strict",
		Extractor: func(c *fiber.Ctx) (string, error) {
			if tok := c.Get("X-Csrf-Token"); tok != "" {
				return tok, nil
			}
			if tok := c.FormValue("csrf_token"); tok != "" {
				return tok, nil
			}
			return "", fmt.Errorf("CSRF token not found")
		},
	})

	requireClerkAdmin := middleware.RequireRole("clerk", "admin")
	requireModAdmin   := middleware.RequireRole("moderator", "admin")
	// requireInstrAdmin guards routes that require the "manager" role.
	// The prompt specifies a "manager" role for return/refund approvals; this system
	// maps that to "instructor" (same capabilities).  "manager" is also accepted
	// explicitly so future user accounts created with role="manager" work without
	// code changes.
	requireInstrAdmin := middleware.RequireRole("instructor", "manager", "admin")
	requireAdmin      := middleware.RequireRole("admin")

	// ------------------------------------------------------------------
	// Protected routes (any authenticated role)
	// ------------------------------------------------------------------

	// Mandatory password rotation — accessible immediately after login even
	// when MustChangePassword == 1 (before the dashboard redirect lands).
	app.Get("/account/change-password",  requireAuth, csrfMiddleware, authHandler.ChangePasswordPage)
	app.Post("/account/change-password", requireAuth, csrfMiddleware, authHandler.ChangePassword)

	app.Post("/logout", requireAuth, csrfMiddleware, authHandler.Logout)

	app.Get("/dashboard", requireAuth, csrfMiddleware, func(c *fiber.Ctx) error {
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

	app.Get("/history", requireAuth, csrfMiddleware, materialHandler.BrowseHistory)

	app.Get("/materials",        requireAuth, csrfMiddleware, materialHandler.ListPage)
	app.Get("/materials/search", requireAuth, csrfMiddleware, materialHandler.SearchPartial)
	app.Get("/materials/:id",    requireAuth, csrfMiddleware, materialHandler.DetailPage)

	app.Post("/materials/:id/rate",     requireAuth, csrfMiddleware, materialHandler.Rate)
	app.Post("/materials/:id/comments", requireAuth, csrfMiddleware, commentRateLimit, materialHandler.AddComment)
	app.Post("/comments/:id/report",    requireAuth, csrfMiddleware, materialHandler.ReportComment)

	app.Get("/favorites",                          requireAuth, csrfMiddleware, materialHandler.FavoritesList)
	app.Post("/favorites",                         requireAuth, csrfMiddleware, materialHandler.CreateFavoritesList)
	app.Get("/favorites/:id",                      requireAuth, csrfMiddleware, materialHandler.GetFavoritesListDetail)
	app.Get("/favorites/:id/items",                requireAuth, csrfMiddleware, materialHandler.GetFavoritesListItems)
	app.Post("/favorites/:id/items",               requireAuth, csrfMiddleware, materialHandler.AddToFavorites)
	app.Delete("/favorites/:id/items/:materialID", requireAuth, csrfMiddleware, materialHandler.RemoveFromFavorites)
	app.Get("/favorites/:id/share",                requireAuth, csrfMiddleware, materialHandler.ShareFavoritesList)

	app.Get("/orders",      requireAuth, csrfMiddleware, orderHandler.ListOrders)
	app.Get("/orders/cart", requireAuth, csrfMiddleware, orderHandler.CartPage)
	app.Get("/orders/:id",  requireAuth, csrfMiddleware, orderHandler.OrderDetail)

	app.Post("/orders",             requireAuth, csrfMiddleware, orderHandler.PlaceOrder)
	app.Post("/orders/:id/pay",     requireAuth, csrfMiddleware, orderHandler.ConfirmPayment)
	app.Post("/orders/:id/cancel",  requireAuth, csrfMiddleware, orderHandler.CancelOrder)
	app.Post("/orders/:id/returns", requireAuth, csrfMiddleware, orderHandler.SubmitReturnRequest)

	app.Get("/returns", requireAuth, csrfMiddleware, orderHandler.ListReturnRequests)

	app.Get("/inbox",               requireAuth, csrfMiddleware, messagingHandler.Inbox)
	app.Get("/inbox/items",         requireAuth, csrfMiddleware, messagingHandler.InboxItems)
	app.Post("/inbox/:id/read",     requireAuth, csrfMiddleware, messagingHandler.MarkRead)
	app.Post("/inbox/read-all",     requireAuth, csrfMiddleware, messagingHandler.MarkAllRead)
	app.Get("/inbox/settings",      requireAuth, csrfMiddleware, messagingHandler.Settings)
	app.Post("/inbox/settings/dnd", requireAuth, csrfMiddleware, messagingHandler.UpdateDND)
	app.Post("/inbox/subscribe",    requireAuth, csrfMiddleware, messagingHandler.Subscribe)
	app.Post("/inbox/unsubscribe",  requireAuth, csrfMiddleware, messagingHandler.Unsubscribe)
	app.Get("/inbox/badge",            requireAuth, csrfMiddleware, messagingHandler.Badge)
	app.Get("/api/inbox/unread-count", requireAuth, csrfMiddleware, messagingHandler.Badge)
	// SSE endpoint — GET only, no CSRF (safe method; EventSource cannot send custom headers).
	app.Get("/inbox/sse", requireAuth, messagingHandler.InboxSSE)

	app.Get("/api/stats/:stat", requireAuth, csrfMiddleware, analyticsHandler.DashboardStat)

	// ------------------------------------------------------------------
	// Clerk / Admin routes
	// ------------------------------------------------------------------
	app.Get("/distribution",                 requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.PickList)
	app.Post("/distribution/issue",          requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.IssueItems)
	app.Post("/distribution/return",         requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.RecordReturn)
	app.Post("/distribution/exchange",       requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.RecordExchange)
	app.Get("/distribution/reissue",         requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.ReissueForm)
	app.Post("/distribution/reissue",        requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.ReissueItem)
	app.Get("/distribution/ledger",          requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.Ledger)
	app.Get("/distribution/ledger/search",   requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.LedgerSearch)
	app.Get("/distribution/custody/:scanID", requireAuth, csrfMiddleware, requireClerkAdmin, distributionHandler.CustodyChain)

	app.Get("/admin/orders",              requireAuth, csrfMiddleware, requireClerkAdmin, orderHandler.AdminListOrders)
	app.Post("/admin/orders/:id/ship",    requireAuth, csrfMiddleware, requireClerkAdmin, orderHandler.MarkShipped)
	app.Post("/admin/orders/:id/deliver", requireAuth, csrfMiddleware, requireClerkAdmin, orderHandler.MarkDelivered)

	// ------------------------------------------------------------------
	// Moderator routes
	// ------------------------------------------------------------------
	app.Get("/moderation",              requireAuth, csrfMiddleware, requireModAdmin, moderationHandler.Queue)
	app.Get("/moderation/items",        requireAuth, csrfMiddleware, requireModAdmin, moderationHandler.QueueItems)
	app.Post("/moderation/:id/approve", requireAuth, csrfMiddleware, requireModAdmin, moderationHandler.Approve)
	app.Post("/moderation/:id/remove",  requireAuth, csrfMiddleware, requireModAdmin, moderationHandler.Remove)

	// ------------------------------------------------------------------
	// Instructor / Admin routes
	// ------------------------------------------------------------------
	app.Get("/dashboard/instructor",          requireAuth, csrfMiddleware, requireInstrAdmin, analyticsHandler.InstructorDashboard)
	app.Get("/admin/returns",                 requireAuth, csrfMiddleware, requireInstrAdmin, orderHandler.AdminListReturnRequests)
	app.Post("/admin/returns/:id/approve",    requireAuth, csrfMiddleware, requireInstrAdmin, orderHandler.ApproveReturn)
	app.Post("/admin/returns/:id/reject",     requireAuth, csrfMiddleware, requireInstrAdmin, orderHandler.RejectReturn)
	app.Post("/admin/orders/:id/cancel",      requireAuth, csrfMiddleware, requireInstrAdmin, orderHandler.AdminCancelOrder)

	app.Get("/courses",                            requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.ListCourses)
	app.Get("/courses/new",                        requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.NewCourseForm)
	app.Post("/courses",                           requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.CreateCourse)
	app.Get("/courses/:id",                        requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.CourseDetail)
	app.Post("/courses/:id/plan",                  requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.AddPlanItem)
	app.Post("/courses/:id/plan/:planID/approve",  requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.ApprovePlanItem)
	app.Post("/courses/:id/sections",              requireAuth, csrfMiddleware, requireInstrAdmin, courseHandler.AddSection)

	// ------------------------------------------------------------------
	// Admin-only routes
	// ------------------------------------------------------------------
	app.Get("/dashboard/admin", requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.AdminDashboard)

	app.Get("/admin/materials/new",      requireAuth, csrfMiddleware, requireAdmin, materialHandler.NewMaterialForm)
	app.Post("/admin/materials",         requireAuth, csrfMiddleware, requireAdmin, materialHandler.CreateMaterial)
	app.Get("/admin/materials/:id/edit", requireAuth, csrfMiddleware, requireAdmin, materialHandler.EditMaterialForm)
	app.Put("/admin/materials/:id",      requireAuth, csrfMiddleware, requireAdmin, materialHandler.UpdateMaterial)
	app.Delete("/admin/materials/:id",   requireAuth, csrfMiddleware, requireAdmin, materialHandler.DeleteMaterial)

	app.Get("/admin/users",                     requireAuth, csrfMiddleware, requireAdmin, adminHandler.ListUsers)
	app.Get("/admin/users/new",                 requireAuth, csrfMiddleware, requireAdmin, adminHandler.NewUserForm)
	app.Post("/admin/users",                    requireAuth, csrfMiddleware, requireAdmin, adminHandler.CreateUser)
	app.Get("/admin/users/:id",                 requireAuth, csrfMiddleware, requireAdmin, adminHandler.UserProfile)
	app.Post("/admin/users/:id/role",           requireAuth, csrfMiddleware, requireAdmin, adminHandler.UpdateRole)
	app.Post("/admin/users/:id/unlock",         requireAuth, csrfMiddleware, requireAdmin, adminHandler.UnlockUser)
	// Generic entity custom fields — entity_type may be: user, course, material, location
	app.Get("/admin/fields/:entity_type/:entity_id",          requireAuth, csrfMiddleware, requireAdmin, adminHandler.CustomFieldsPage)
	app.Post("/admin/fields/:entity_type/:entity_id",         requireAuth, csrfMiddleware, requireAdmin, adminHandler.SetCustomField)
	app.Delete("/admin/fields/:entity_type/:entity_id/:name", requireAuth, csrfMiddleware, requireAdmin, adminHandler.DeleteCustomField)
	// Legacy user-scoped aliases — kept for backward compatibility with existing links
	app.Get("/admin/users/:id/fields",          requireAuth, csrfMiddleware, requireAdmin, adminHandler.CustomFieldsPage)
	app.Post("/admin/users/:id/fields",         requireAuth, csrfMiddleware, requireAdmin, adminHandler.SetCustomField)
	app.Delete("/admin/users/:id/fields/:name", requireAuth, csrfMiddleware, requireAdmin, adminHandler.DeleteCustomField)

	app.Get("/admin/duplicates",        requireAuth, csrfMiddleware, requireAdmin, adminHandler.DuplicatesPage)
	app.Post("/admin/duplicates/merge", requireAuth, csrfMiddleware, requireAdmin, adminHandler.MergeUsers)

	app.Get("/admin/audit",                       requireAuth, csrfMiddleware, requireAdmin, adminHandler.AuditLogPage)
	app.Get("/admin/audit/:entityType/:entityID", requireAuth, csrfMiddleware, requireAdmin, adminHandler.EntityAuditLog)

	app.Get("/analytics/map",                        requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.MapPage)
	app.Get("/analytics/map/data",                   requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.MapData)
	app.Post("/analytics/map/compute",               requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.ComputeGrid)
	app.Get("/analytics/map/buffer",                 requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.BufferQuery)
	app.Get("/analytics/map/poi-density",            requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.POIDensity)
	app.Get("/analytics/map/trajectory/:materialID", requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.Trajectory)
	app.Get("/analytics/map/regions",                requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.RegionAggregate)
	app.Post("/analytics/map/regions/compute",       requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.ComputeRegions)
	app.Get("/analytics/export/orders",              requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.ExportOrders)
	app.Get("/analytics/export/distribution",        requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.ExportDistribution)
	app.Get("/analytics/kpi/:name",                  requireAuth, csrfMiddleware, requireAdmin, analyticsHandler.KPIHistory)

	// ------------------------------------------------------------------ //
	// 11. Graceful shutdown                                               //
	// ------------------------------------------------------------------ //
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		addr := ":" + cfg.Port
		log.Printf("main: listening on %s (env=%s)", addr, cfg.AppEnv)
		if err := app.Listen(addr); err != nil {
			log.Printf("main: server error: %v", err)
		}
	}()

	<-quit
	log.Println("main: shutting down...")

	if err := app.Shutdown(); err != nil {
		log.Printf("main: shutdown error: %v", err)
	}

	log.Println("main: shutdown complete")
}
