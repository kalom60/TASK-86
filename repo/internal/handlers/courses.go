package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/middleware"
	"w2t86/internal/observability"
	"w2t86/internal/services"
)

// CourseHandler handles all course-planning HTTP routes.
type CourseHandler struct {
	courseService *services.CourseService
}

// NewCourseHandler creates a CourseHandler backed by the given service.
func NewCourseHandler(cs *services.CourseService) *CourseHandler {
	return &CourseHandler{courseService: cs}
}

// ListCourses handles GET /courses — renders the course list for the current instructor.
func (h *CourseHandler) ListCourses(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	courses, err := h.courseService.ListCourses(user.ID)
	if err != nil {
		return internalErr(c, observability.App, "list courses failed", err, "user_id", user.ID)
	}

	return c.Render("courses/list", fiber.Map{
		"Title":      "My Courses",
		"User":       user,
		"Courses":    courses,
		"ActivePage": "courses",
	}, "layouts/base")
}

// NewCourseForm handles GET /courses/new.
func (h *CourseHandler) NewCourseForm(c *fiber.Ctx) error {
	user := middleware.GetUser(c)
	return c.Render("courses/new", fiber.Map{
		"Title":      "New Course",
		"User":       user,
		"ActivePage": "courses",
	}, "layouts/base")
}

// CreateCourse handles POST /courses.
func (h *CourseHandler) CreateCourse(c *fiber.Ctx) error {
	user := middleware.GetUser(c)

	name := c.FormValue("name")
	subject := c.FormValue("subject")
	grade := c.FormValue("grade_level")
	year := c.FormValue("academic_year")

	course, err := h.courseService.CreateCourse(user.ID, name, subject, grade, year)
	if err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not create course. Please check your input.")
	}

	observability.App.Info("course created", "course_id", course.ID, "instructor_id", user.ID)
	return c.Redirect("/courses/"+strconv.FormatInt(course.ID, 10), fiber.StatusFound)
}

// CourseDetail handles GET /courses/:id — renders the course detail with its plan.
func (h *CourseHandler) CourseDetail(c *fiber.Ctx) error {
	id, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid course ID")
	}

	user := middleware.GetUser(c)
	isAdmin := user.Role == "admin"

	course, err := h.courseService.GetCourse(int64(id), user.ID, isAdmin)
	if err != nil {
		return apiErr(c, fiber.StatusNotFound, "Course not found")
	}

	sections, err := h.courseService.GetSections(int64(id))
	if err != nil {
		observability.App.Warn("get course sections failed", "course_id", id, "error", err)
		sections = nil
	}

	planItems, err := h.courseService.GetPlanItems(int64(id))
	if err != nil {
		observability.App.Warn("get plan items failed", "course_id", id, "error", err)
		planItems = nil
	}

	return c.Render("courses/detail", fiber.Map{
		"Title":      course.Name,
		"User":       user,
		"Course":     course,
		"Sections":   sections,
		"PlanItems":  planItems,
		"ActivePage": "courses",
	}, "layouts/base")
}

// AddPlanItem handles POST /courses/:id/plan — adds a material demand line.
func (h *CourseHandler) AddPlanItem(c *fiber.Ctx) error {
	courseID, err := c.ParamsInt("id")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid course ID")
	}

	materialID, err := strconv.ParseInt(c.FormValue("material_id"), 10, 64)
	if err != nil || materialID <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Invalid material ID")
	}

	qty, err := strconv.Atoi(c.FormValue("requested_qty"))
	if err != nil || qty <= 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Requested quantity must be a positive number")
	}

	notes := c.FormValue("notes")

	var sectionID *int64
	if raw := c.FormValue("section_id"); raw != "" {
		if n, err2 := strconv.ParseInt(raw, 10, 64); err2 == nil && n > 0 {
			sectionID = &n
		}
	}

	planItem, err := h.courseService.AddPlanItem(int64(courseID), materialID, sectionID, qty, notes)
	if err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not add plan item: "+err.Error())
	}

	observability.App.Info("course plan item added", "course_id", courseID, "material_id", materialID, "plan_id", planItem.ID)

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Material added to course plan.",
		})
	}
	return c.Redirect("/courses/"+strconv.Itoa(courseID), fiber.StatusFound)
}

// ApprovePlanItem handles POST /courses/:id/plan/:planID/approve (admin).
func (h *CourseHandler) ApprovePlanItem(c *fiber.Ctx) error {
	planID, err := c.ParamsInt("planID")
	if err != nil {
		return apiErr(c, fiber.StatusBadRequest, "Invalid plan ID")
	}

	qty, err := strconv.Atoi(c.FormValue("approved_qty"))
	if err != nil || qty < 0 {
		return htmxErr(c, fiber.StatusBadRequest, "Approved quantity must be non-negative")
	}

	if err := h.courseService.ApprovePlanItem(int64(planID), qty); err != nil {
		return htmxErr(c, fiber.StatusUnprocessableEntity, "Could not approve plan item.")
	}

	if c.Get("HX-Request") == "true" {
		return c.Render("partials/flash", fiber.Map{
			"Message": "Plan item approved.",
		})
	}
	return c.Redirect(c.Get("Referer", "/courses"), fiber.StatusFound)
}
