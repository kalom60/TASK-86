package handlers

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/models"
	"w2t86/internal/observability"
	"w2t86/internal/repository"
	"w2t86/internal/services"
)

// MaterialHandler handles all materials-related HTTP routes.
type MaterialHandler struct {
	materialService *services.MaterialService
	authMiddleware  *middleware.AuthMiddleware
}

// NewMaterialHandler creates a MaterialHandler backed by the given service.
func NewMaterialHandler(ms *services.MaterialService) *MaterialHandler {
	return &MaterialHandler{materialService: ms}
}

// ---------------------------------------------------------------
// Public / student routes
// ---------------------------------------------------------------

// ListPage handles GET /materials — renders the full materials search page.
func (h *MaterialHandler) ListPage(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	return c.Render("materials/list", fiber.Map{
		"Title": "Materials Catalog",
		"User":  user,
	}, "layouts/main")
}

// SearchPartial handles GET /materials/search — HTMX partial returning material cards.
// Accepts query params: q, subject, grade.
func (h *MaterialHandler) SearchPartial(c *fiber.Ctx) error {
	q := c.Query("q")
	subject := c.Query("subject")
	grade := c.Query("grade")

	limit := 24
	offset := 0
	if p := c.QueryInt("page", 1); p > 1 {
		offset = (p - 1) * limit
	}

	filters := map[string]string{}
	if subject != "" {
		filters["subject"] = subject
	}
	if grade != "" {
		filters["grade_level"] = grade
	}

	materials, err := h.materialService.Search(q, filters, limit, offset)
	if err != nil {
		return internalErr(c, observability.App, "material search failed", err, "query", q)
	}

	return c.Render("partials/material_cards", fiber.Map{
		"Materials": materials,
		"Query":     q,
	})
}

// DetailPage handles GET /materials/:id — renders the full material detail page.
// Records browse history for authenticated users.
func (h *MaterialHandler) DetailPage(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	material, err := h.materialService.GetByID(int64(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).Render("materials/detail", fiber.Map{
			"Title": "Not Found",
			"Error": "Material not found",
		}, "layouts/main")
	}

	user := middleware.GetUser(c)

	// Best-effort browse history recording (non-fatal).
	if user != nil {
		if err := h.materialService.RecordVisit(int64(id), user.ID); err != nil {
			observability.App.Warn("record visit failed", "material_id", id, "user_id", user.ID, "error", err)
		}
	}

	// Load average rating and user's own rating.
	avgRating, ratingCount, err := h.materialService.GetAverageRating(int64(id))
	if err != nil {
		observability.App.Warn("get average rating failed", "material_id", id, "error", err)
		avgRating, ratingCount = 0, 0
	}
	var userStars int
	if user != nil {
		userStars, err = h.materialService.GetUserRating(int64(id), user.ID)
		if err != nil {
			observability.App.Warn("get user rating failed", "material_id", id, "user_id", user.ID, "error", err)
			userStars = 0
		}
	}

	// Load comments (include collapsed for the owner / admin; exclude for others).
	includeCollapsed := user != nil && (user.Role == "admin" || user.Role == "moderator")
	comments, err := h.materialService.GetComments(int64(id), includeCollapsed, 50, 0)
	if err != nil {
		observability.App.Warn("get comments failed", "material_id", id, "error", err)
		comments = nil
	}

	// Load the authenticated user's favorites lists for the add-to-favorites widget.
	var favoritesLists interface{}
	if user != nil {
		lists, err := h.materialService.GetFavoritesLists(user.ID)
		if err != nil {
			observability.App.Warn("get favorites lists for detail page failed", "user_id", user.ID, "error", err)
		} else {
			favoritesLists = lists
		}
	}

	return c.Render("materials/detail", fiber.Map{
		"Title":          material.Title,
		"Material":       material,
		"User":           user,
		"AvgRating":      fmt.Sprintf("%.1f", avgRating),
		"RatingCount":    ratingCount,
		"UserStars":      userStars,
		"Comments":       comments,
		"FavoritesLists": favoritesLists,
	}, "layouts/main")
}

// Rate handles POST /materials/:id/rate — records a star rating via HTMX.
func (h *MaterialHandler) Rate(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	user := middleware.GetUser(c)
	if user == nil {
		observability.Security.Warn("unauthenticated access attempt", "path", c.Path(), "ip", c.IP())
		return apiErr(c, fiber.StatusUnauthorized, "Authentication required")
	}

	starsStr := c.FormValue("stars")
	stars, err := strconv.Atoi(starsStr)
	if err != nil || stars < 1 || stars > 5 {
		return htmxErr(c, fiber.StatusBadRequest, "Stars must be between 1 and 5")
	}

	if err := h.materialService.Rate(int64(id), user.ID, stars); err != nil {
		return internalErr(c, observability.App, "rate material failed", err, "material_id", id, "user_id", user.ID)
	}

	avg, count, err := h.materialService.GetAverageRating(int64(id))
	if err != nil {
		observability.App.Warn("get average rating failed", "material_id", id, "error", err)
		avg, count = 0, 0
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/star_widget", fiber.Map{
			"MaterialID":  id,
			"AvgRating":   fmt.Sprintf("%.1f", avg),
			"RatingCount": count,
			"UserStars":   stars,
		})
	}
	return c.Redirect(fmt.Sprintf("/materials/%d", id), fiber.StatusFound)
}

// AddComment handles POST /materials/:id/comments — posts a comment via HTMX.
func (h *MaterialHandler) AddComment(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	user := middleware.GetUser(c)
	if user == nil {
		observability.Security.Warn("unauthenticated access attempt", "path", c.Path(), "ip", c.IP())
		return apiErr(c, fiber.StatusUnauthorized, "Authentication required")
	}

	body := c.FormValue("body")

	comment, err := h.materialService.AddComment(int64(id), user.ID, body)
	if err != nil {
		// The service returns user-facing validation errors for rate limit and
		// sensitive words. Log the specific cause then return a fixed message.
		observability.App.Warn("add comment rejected", "material_id", id, "user_id", user.ID, "error", err)
		if c.Get("HX-Request") == "true" {
			return c.Status(fiber.StatusUnprocessableEntity).Render("partials/comment_form", fiber.Map{
				"MaterialID": id,
				"Error":      "Your comment could not be submitted. Please check your input and try again.",
				"Body":       body,
			})
		}
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Your comment could not be submitted. Please check your input and try again.")
	}

	if c.Get("HX-Request") == "true" {
		comments, err := h.materialService.GetComments(int64(id), false, 50, 0)
		if err != nil {
			observability.App.Warn("get comments failed after add", "material_id", id, "error", err)
			comments = nil
		}
		return c.Render("partials/comments_list", fiber.Map{
			"MaterialID": id,
			"Comments":   comments,
			"NewComment": comment,
			"User":       user,
		})
	}
	return c.Redirect(fmt.Sprintf("/materials/%d", id), fiber.StatusFound)
}

// ReportComment handles POST /comments/:id/report — reports a comment via HTMX.
func (h *MaterialHandler) ReportComment(c *fiber.Ctx) error {
	commentID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid comment ID")
	}

	user := middleware.GetUser(c)
	if user == nil {
		observability.Security.Warn("unauthenticated access attempt", "path", c.Path(), "ip", c.IP())
		return apiErr(c, fiber.StatusUnauthorized, "Authentication required")
	}

	reason := c.FormValue("reason")

	if err := h.materialService.ReportComment(int64(commentID), user.ID, reason); err != nil {
		return internalErr(c, observability.App, "report comment failed", err, "comment_id", commentID, "user_id", user.ID)
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/comment_reported", fiber.Map{
			"CommentID": commentID,
			"Message":   "Report submitted. Thank you.",
		})
	}
	return c.JSON(fiber.Map{"msg": "Report submitted"})
}

// ---------------------------------------------------------------
// Admin routes
// ---------------------------------------------------------------

// NewMaterialForm handles GET /admin/materials/new.
func (h *MaterialHandler) NewMaterialForm(c *fiber.Ctx) error {
	return c.Render("admin/materials/new", fiber.Map{
		"Title": "Add Material",
		"User":  middleware.GetUser(c),
	}, "layouts/main")
}

// CreateMaterial handles POST /admin/materials.
func (h *MaterialHandler) CreateMaterial(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	m := materialModelFromForm(c)

	created, err := h.materialService.CreateMaterial(m, user.ID)
	if err != nil {
		observability.App.Warn("create material rejected", "user_id", user.ID, "error", err)
		data := fiber.Map{
			"Title":    "Add Material",
			"Error":    "Could not create material. Please check your input and try again.",
			"Material": m,
			"User":     user,
		}
		if c.Get("HX-Request") == "true" {
			return c.Status(fiber.StatusUnprocessableEntity).Render("admin/materials/new", data)
		}
		return c.Status(fiber.StatusUnprocessableEntity).Render("admin/materials/new", data, "layouts/main")
	}

	return c.Redirect(fmt.Sprintf("/materials/%d", created.ID), fiber.StatusFound)
}

// EditMaterialForm handles GET /admin/materials/:id/edit.
func (h *MaterialHandler) EditMaterialForm(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	material, err := h.materialService.GetByID(int64(id))
	if err != nil {
		return apiErr(c, fiber.StatusNotFound, "Material not found")
	}

	return c.Render("admin/materials/edit", fiber.Map{
		"Title":    "Edit Material",
		"Material": material,
		"User":     middleware.GetUser(c),
	}, "layouts/main")
}

// UpdateMaterial handles PUT /admin/materials/:id.
func (h *MaterialHandler) UpdateMaterial(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	user := middleware.GetUser(c)
	fields := materialFieldsFromForm(c)

	if err := h.materialService.UpdateMaterial(int64(id), fields, user.ID); err != nil {
		return internalErr(c, observability.App, "update material failed", err, "material_id", id, "user_id", user.ID)
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Material updated successfully."})
	}
	return c.Redirect(fmt.Sprintf("/materials/%d", id), fiber.StatusFound)
}

// DeleteMaterial handles DELETE /admin/materials/:id — soft deletes a material.
func (h *MaterialHandler) DeleteMaterial(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	if err := h.materialService.SoftDelete(int64(id)); err != nil {
		return internalErr(c, observability.App, "delete material failed", err, "material_id", id)
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Material deleted."})
	}
	return c.Redirect("/materials", fiber.StatusFound)
}

// ---------------------------------------------------------------
// Favorites routes
// ---------------------------------------------------------------

// FavoritesList handles GET /favorites — renders the user's favorites lists.
func (h *MaterialHandler) FavoritesList(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	lists, err := h.materialService.GetFavoritesLists(user.ID)
	if err != nil {
		observability.App.Warn("get favorites lists failed", "user_id", user.ID, "error", err)
		lists = nil
	}

	return c.Render("favorites/list", fiber.Map{
		"Title": "My Favorites",
		"User":  user,
		"Lists": lists,
	}, "layouts/main")
}

// CreateFavoritesList handles POST /favorites — creates a new favorites list.
func (h *MaterialHandler) CreateFavoritesList(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	name := c.FormValue("name")
	visibility := c.FormValue("visibility")
	if visibility == "" {
		visibility = "private"
	}

	list, err := h.materialService.CreateFavoritesList(user.ID, name, visibility)
	if err != nil {
		observability.App.Warn("create favorites list rejected", "user_id", user.ID, "error", err)
		if c.Get("HX-Request") == "true" {
			return c.Status(fiber.StatusUnprocessableEntity).Render("partials/create_list_form", fiber.Map{
				"Error": "Could not create list. Please check your input and try again.",
				"User":  user,
			})
		}
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not create list. Please check your input and try again.")
	}

	if c.Get("HX-Request") == "true" {
		lists, err := h.materialService.GetFavoritesLists(user.ID)
		if err != nil {
			observability.App.Warn("get favorites lists failed", "user_id", user.ID, "error", err)
			lists = nil
		}
		return c.Render("partials/favorites_lists", fiber.Map{
			"Lists":   lists,
			"NewList": list,
			"User":    user,
		})
	}
	return c.Redirect("/favorites", fiber.StatusFound)
}

// AddToFavorites handles POST /favorites/:id/items — adds a material to a list.
func (h *MaterialHandler) AddToFavorites(c *fiber.Ctx) error {
	listID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid list ID")
	}

	user := middleware.GetUser(c)
	materialIDStr := c.FormValue("material_id")
	materialID, err := strconv.ParseInt(materialIDStr, 10, 64)
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	if err := h.materialService.AddToFavorites(int64(listID), materialID, user.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not add to favorites. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{"Message": "Added to favorites."})
	}
	return c.Redirect("/favorites", fiber.StatusFound)
}

// RemoveFromFavorites handles DELETE /favorites/:id/items/:materialID.
func (h *MaterialHandler) RemoveFromFavorites(c *fiber.Ctx) error {
	listID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid list ID")
	}
	materialID, err := c.ParamsInt("materialID")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	user := middleware.GetUser(c)
	if err := h.materialService.RemoveFromFavorites(int64(listID), int64(materialID), user.ID); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not remove from favorites. Please try again.")
	}

	if c.Get("HX-Request") == "true" {
		items, err := h.materialService.GetListItems(int64(listID))
		if err != nil {
			observability.App.Warn("get list items failed", "list_id", listID, "error", err)
			items = nil
		}
		return c.Render("partials/favorites_items", fiber.Map{
			"ListID": listID,
			"Items":  items,
		})
	}
	return c.Redirect("/favorites", fiber.StatusFound)
}

// GetFavoritesListDetail handles GET /favorites/:id — full-page view of a list.
func (h *MaterialHandler) GetFavoritesListDetail(c *fiber.Ctx) error {
	listID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid list ID")
	}

	user := middleware.GetUser(c)

	list, err := h.materialService.GetFavoritesListByID(int64(listID))
	if err != nil || list.UserID != user.ID {
		return apiErr(c, fiber.StatusNotFound, "List not found")
	}

	items, err := h.materialService.GetListItems(int64(listID))
	if err != nil {
		observability.App.Warn("get list items failed", "list_id", listID, "error", err)
		items = nil
	}

	return c.Render("favorites/detail", fiber.Map{
		"Title":  list.Name,
		"User":   user,
		"List":   list,
		"Items":  items,
		"ListID": listID,
	}, "layouts/main")
}

// GetFavoritesListItems handles GET /favorites/:id/items — HTMX partial returning list items.
func (h *MaterialHandler) GetFavoritesListItems(c *fiber.Ctx) error {
	listID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid list ID")
	}

	user := middleware.GetUser(c)

	list, err := h.materialService.GetFavoritesListByID(int64(listID))
	if err != nil || list.UserID != user.ID {
		return apiErr(c, fiber.StatusNotFound, "List not found")
	}

	items, err := h.materialService.GetListItems(int64(listID))
	if err != nil {
		observability.App.Warn("get list items partial failed", "list_id", listID, "error", err)
		items = nil
	}

	return c.Render("partials/favorites_items", fiber.Map{
		"ListID": listID,
		"Items":  items,
	})
}

// ShareFavoritesList handles GET /favorites/:id/share — generates a share link.
func (h *MaterialHandler) ShareFavoritesList(c *fiber.Ctx) error {
	listID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid list ID")
	}

	user := middleware.GetUser(c)
	token, err := h.materialService.GetShareLink(int64(listID), user.ID)
	if err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not generate share link. Please try again.")
	}

	shareURL := fmt.Sprintf("%s/share/%s", baseURL(c), token)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/share_link", fiber.Map{
			"ShareURL": shareURL,
			"Token":    token,
		})
	}
	return c.Render("favorites/share", fiber.Map{
		"Title":    "Share List",
		"ShareURL": shareURL,
		"User":     user,
	}, "layouts/main")
}

// SharedList handles GET /share/:token — publicly accessible shared favorites list.
func (h *MaterialHandler) SharedList(c *fiber.Ctx) error {
	token := c.Params("token")

	list, err := h.materialService.GetListByShareToken(token)
	if err != nil {
		status := fiber.StatusNotFound
		if errors.Is(err, repository.ErrShareExpired) {
			status = fiber.StatusGone // 410: token exists but expired
		}
		return c.Status(status).Render("shared/expired", fiber.Map{
			"Title": "Link Expired",
		}, "layouts/main")
	}

	items, err := h.materialService.GetListItems(list.ID)
	if err != nil {
		observability.App.Warn("get shared list items failed", "list_id", list.ID, "error", err)
		items = nil
	}
	// Resolve material details for each item.
	var enriched []fiber.Map
	for _, item := range items {
		mat, _ := h.materialService.GetByID(item.MaterialID)
		enriched = append(enriched, fiber.Map{
			"Item":     item,
			"Material": mat,
		})
	}

	return c.Render("favorites/shared", fiber.Map{
		"Title": list.Name + " — Shared List",
		"List":  list,
		"Items": enriched,
	}, "layouts/main")
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------
// Browse history
// ---------------------------------------------------------------

// BrowseHistory handles GET /history — renders the "Resume Browsing" page with
// the authenticated user's recently visited materials.
func (h *MaterialHandler) BrowseHistory(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	items, err := h.materialService.GetBrowseHistoryItems(user.ID, 50)
	if err != nil {
		observability.App.Warn("get browse history failed", "user_id", user.ID, "error", err)
		items = nil
	}

	return c.Render("history/list", fiber.Map{
		"Title":      "Resume Browsing",
		"User":       user,
		"History":    items,
		"ActivePage": "history",
	}, "layouts/base")
}

// ---------------------------------------------------------------

func materialModelFromForm(c *fiber.Ctx) *models.Material {
	m := &models.Material{
		Title:        c.FormValue("title"),
		TotalQty:     intFormValue(c, "total_qty"),
		AvailableQty: intFormValue(c, "available_qty"),
		ReservedQty:  0,
		Status:       c.FormValue("status"),
	}
	if v := c.FormValue("isbn"); v != "" {
		m.ISBN = &v
	}
	if v := c.FormValue("author"); v != "" {
		m.Author = &v
	}
	if v := c.FormValue("publisher"); v != "" {
		m.Publisher = &v
	}
	if v := c.FormValue("edition"); v != "" {
		m.Edition = &v
	}
	if v := c.FormValue("subject"); v != "" {
		m.Subject = &v
	}
	if v := c.FormValue("grade_level"); v != "" {
		m.GradeLevel = &v
	}
	if m.Status == "" {
		m.Status = "active"
	}
	return m
}

func materialFieldsFromForm(c *fiber.Ctx) map[string]interface{} {
	fields := map[string]interface{}{}
	stringFields := []string{"isbn", "title", "author", "publisher", "edition", "subject", "grade_level", "status"}
	for _, f := range stringFields {
		if v := c.FormValue(f); v != "" {
			fields[f] = v
		}
	}
	intFields := []string{"total_qty", "available_qty"}
	for _, f := range intFields {
		if v := c.FormValue(f); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				fields[f] = n
			}
		}
	}
	return fields
}

func intFormValue(c *fiber.Ctx, key string) int {
	n, _ := strconv.Atoi(c.FormValue(key))
	return n
}

func baseURL(c *fiber.Ctx) string {
	scheme := "https"
	if c.Protocol() == "http" {
		scheme = "http"
	}
	return scheme + "://" + c.Hostname()
}
