package handlers

import (
	"encoding/hex"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/services"
)

// AdminHandler serves all admin UI routes: user management, custom fields,
// duplicate detection, merging, and audit log.
type AdminHandler struct {
	adminService *services.AdminService
	authService  *services.AuthService
}

// NewAdminHandler creates an AdminHandler backed by the given services.
func NewAdminHandler(as *services.AdminService, aus *services.AuthService) *AdminHandler {
	return &AdminHandler{adminService: as, authService: aus}
}

// ---------------------------------------------------------------
// User list
// ---------------------------------------------------------------

// ListUsers handles GET /admin/users — paginated user list with optional role filter.
func (h *AdminHandler) ListUsers(c *fiber.Ctx) error {
	role := c.Query("role")
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	users, err := h.adminService.ListUsers(role, limit, offset)
	if err != nil {
		observability.App.Warn("list users failed", "role_filter", role, "error", err)
		users = nil
	}

	return c.Render("admin/users/list", fiber.Map{
		"Title":      "User Management",
		"User":       middleware.GetUser(c),
		"Users":      users,
		"RoleFilter": role,
		"Page":       page,
	}, "layouts/main")
}

// NewUserForm handles GET /admin/users/new.
func (h *AdminHandler) NewUserForm(c *fiber.Ctx) error {
	return c.Render("admin/users/new", fiber.Map{
		"Title": "Create User",
		"User":  middleware.GetUser(c),
	}, "layouts/main")
}

// CreateUser handles POST /admin/users.
func (h *AdminHandler) CreateUser(c *fiber.Ctx) error {
	username := c.FormValue("username")
	email := c.FormValue("email")
	password := c.FormValue("password")
	role := c.FormValue("role")
	fullName := c.FormValue("full_name")
	if role == "" {
		role = "student"
	}

	user, err := h.adminService.CreateUser(username, email, password, role, fullName)
	if err != nil {
		observability.App.Warn("create user rejected", "username", username, "role", role, "error", err)
		return c.Status(fiber.StatusUnprocessableEntity).Render("admin/users/new", fiber.Map{
			"Title": "Create User",
			"Error": "Could not create user. Please check your input and try again.",
			"User":  middleware.GetUser(c),
		}, "layouts/main")
	}

	return c.Redirect("/admin/users/"+strconv.FormatInt(user.ID, 10), fiber.StatusFound)
}

// ---------------------------------------------------------------
// User profile
// ---------------------------------------------------------------

// UserProfile handles GET /admin/users/:id.
func (h *AdminHandler) UserProfile(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid user ID")
	}

	encKey := encKeyFromContext(c)
	fields, err := h.adminService.GetCustomFields(int64(id), encKey)
	if err != nil {
		observability.App.Warn("get custom fields failed", "target_user_id", id, "error", err)
		fields = nil
	}
	auditLog, err := h.adminService.GetAuditLog("user", int64(id), 20, 0)
	if err != nil {
		observability.App.Warn("get audit log failed", "target_user_id", id, "error", err)
		auditLog = nil
	}

	// Fetch the target user via ListUsers with a high limit and find by ID.
	// (We reuse the admin repo path; a direct GetByID is available via userRepo
	//  but the admin handler only holds adminService. We call ListUsers offset=0
	//  and search — simpler: expose userRepo.GetByID in adminService.)
	users, err := h.adminService.ListUsers("", 10000, 0)
	if err != nil {
		observability.App.Warn("list users for profile failed", "target_user_id", id, "error", err)
		users = nil
	}
	var targetUser interface{}
	for _, u := range users {
		if u.ID == int64(id) {
			targetUser = u
			break
		}
	}

	return c.Render("admin/users/profile", fiber.Map{
		"Title":      "User Profile",
		"User":       middleware.GetUser(c),
		"TargetUser": targetUser,
		"Fields":     fields,
		"AuditLog":   auditLog,
	}, "layouts/main")
}

// UpdateRole handles POST /admin/users/:id/role.
func (h *AdminHandler) UpdateRole(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid user ID")
	}
	role := c.FormValue("role")
	actor := middleware.GetUser(c)

	if err := h.adminService.UpdateUserRole(int64(id), role, actor.ID, c.IP()); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not update role. Please try again.")
	}

	observability.Security.Info("user role changed",
		"target_user_id", id, "new_role", role,
		"actor_id", actor.ID, "actor_ip", c.IP())

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Role updated."})
	}
	return c.Redirect("/admin/users/"+strconv.Itoa(id), fiber.StatusFound)
}

// UnlockUser handles POST /admin/users/:id/unlock.
func (h *AdminHandler) UnlockUser(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid user ID")
	}
	actor := middleware.GetUser(c)

	if err := h.adminService.UnlockUser(int64(id), actor.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not unlock account. Please try again.")
	}

	observability.Security.Info("account unlocked",
		"target_user_id", id,
		"actor_id", actor.ID, "actor_ip", c.IP())

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Account unlocked."})
	}
	return c.Redirect("/admin/users/"+strconv.Itoa(id), fiber.StatusFound)
}

// ---------------------------------------------------------------
// Custom fields
// ---------------------------------------------------------------

// CustomFieldsPage handles GET /admin/users/:id/fields.
func (h *AdminHandler) CustomFieldsPage(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid user ID")
	}

	encKey := encKeyFromContext(c)
	fields, err := h.adminService.GetCustomFields(int64(id), encKey)
	if err != nil {
		observability.App.Warn("get custom fields failed", "target_user_id", id, "error", err)
		fields = nil
	}

	return c.Render("admin/users/profile", fiber.Map{
		"Title":      "Custom Fields",
		"User":       middleware.GetUser(c),
		"TargetID":   id,
		"Fields":     fields,
		"FieldsOnly": true,
	}, "layouts/main")
}

// SetCustomField handles POST /admin/users/:id/fields.
func (h *AdminHandler) SetCustomField(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid user ID")
	}

	name := c.FormValue("field_name")
	value := c.FormValue("field_value")
	encrypt := c.FormValue("encrypt") == "true" || c.FormValue("encrypt") == "1"

	encKey := encKeyFromContext(c)
	if err := h.adminService.SetCustomField(int64(id), name, value, encrypt, encKey); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not save field. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Field saved."})
	}
	return c.Redirect("/admin/users/"+strconv.Itoa(id)+"/fields", fiber.StatusFound)
}

// DeleteCustomField handles DELETE /admin/users/:id/fields/:name.
func (h *AdminHandler) DeleteCustomField(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid user ID")
	}
	name := c.Params("name")

	if err := h.adminService.DeleteCustomField(int64(id), name); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not delete field. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Field deleted."})
	}
	return c.Redirect("/admin/users/"+strconv.Itoa(id)+"/fields", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Duplicate detection
// ---------------------------------------------------------------

// DuplicatesPage handles GET /admin/duplicates.
func (h *AdminHandler) DuplicatesPage(c *fiber.Ctx) error {
	pairs, err := h.adminService.FindDuplicates()
	if err != nil {
		observability.App.Warn("find duplicates failed", "error", err)
		pairs = nil
	}

	mergeHistory, err := h.adminService.GetAuditLog("user", 0, 20, 0)
	if err != nil {
		observability.App.Warn("get merge history failed", "error", err)
		mergeHistory = nil
	}

	return c.Render("admin/duplicates", fiber.Map{
		"Title":        "Duplicate Users",
		"User":         middleware.GetUser(c),
		"Pairs":        pairs,
		"MergeHistory": mergeHistory,
	}, "layouts/main")
}

// MergeUsers handles POST /admin/duplicates/merge.
func (h *AdminHandler) MergeUsers(c *fiber.Ctx) error {
	primaryIDStr := c.FormValue("primary_id")
	duplicateIDStr := c.FormValue("duplicate_id")

	primaryID, err := strconv.ParseInt(primaryIDStr, 10, 64)
	if err != nil || primaryID <= 0 {
		return apiErr(c, fiber.StatusBadRequest, "Invalid primary user ID")
	}
	duplicateID, err := strconv.ParseInt(duplicateIDStr, 10, 64)
	if err != nil || duplicateID <= 0 {
		return apiErr(c, fiber.StatusBadRequest, "Invalid duplicate user ID")
	}
	if primaryID == duplicateID {
		return apiErr(c, fiber.StatusBadRequest, "Primary and duplicate must be different users")
	}

	actor := middleware.GetUser(c)
	if err := h.adminService.MergeUsers(primaryID, duplicateID, actor.ID); err != nil {
		return internalErr(c, observability.App, "merge users failed", err,
			"primary_id", primaryID, "duplicate_id", duplicateID, "actor_id", actor.ID)
	}

	observability.Security.Warn("users merged",
		"primary_id", primaryID, "duplicate_id", duplicateID, "actor_id", actor.ID)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Users merged successfully.",
		})
	}
	return c.Redirect("/admin/duplicates", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------

// AuditLogPage handles GET /admin/audit.
func (h *AdminHandler) AuditLogPage(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 50)
	logs, err := h.adminService.GetRecentAuditLog(limit)
	if err != nil {
		observability.App.Warn("get recent audit log failed", "error", err)
		logs = nil
	}

	return c.Render("admin/audit", fiber.Map{
		"Title": "Audit Log",
		"User":  middleware.GetUser(c),
		"Logs":  logs,
	}, "layouts/main")
}

// EntityAuditLog handles GET /admin/audit/:entityType/:entityID.
func (h *AdminHandler) EntityAuditLog(c *fiber.Ctx) error {
	entityType := c.Params("entityType")
	entityIDStr := c.Params("entityID")
	entityID, err := strconv.ParseInt(entityIDStr, 10, 64)
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid entity ID")
	}

	limit := c.QueryInt("limit", 50)
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	logs, err := h.adminService.GetAuditLog(entityType, entityID, limit, offset)
	if err != nil {
		observability.App.Warn("get entity audit log failed", "entity_type", entityType, "entity_id", entityID, "error", err)
		logs = nil
	}

	return c.Render("admin/audit", fiber.Map{
		"Title":      "Audit Log — " + entityType + " #" + entityIDStr,
		"User":       middleware.GetUser(c),
		"Logs":       logs,
		"EntityType": entityType,
		"EntityID":   entityID,
	}, "layouts/main")
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

// encKeyFromContext derives the 32-byte AES key from the session user context.
// In a real deployment the key would come from the server config; here we
// decode the hex string that was loaded into the Fiber app's Locals during
// startup (set by main.go as "enc_key").
func encKeyFromContext(c *fiber.Ctx) []byte {
	raw, _ := c.Locals("enc_key").(string)
	if raw == "" {
		return nil
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}
