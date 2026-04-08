package services

import (
	"errors"
	"fmt"

	"w2t86/internal/models"
	"w2t86/internal/repository"
)

// CourseService orchestrates course creation, section management, and material
// demand planning for instructor users.
type CourseService struct {
	courseRepo   *repository.CourseRepository
	materialRepo *repository.MaterialRepository
}

// NewCourseService wires the service to its repositories.
func NewCourseService(cr *repository.CourseRepository, mr *repository.MaterialRepository) *CourseService {
	return &CourseService{courseRepo: cr, materialRepo: mr}
}

// CreateCourse registers a new course owned by the given instructor.
func (s *CourseService) CreateCourse(instructorID int64, name, subject, gradeLevel, academicYear string) (*models.Course, error) {
	if name == "" {
		return nil, errors.New("service: CreateCourse: course name is required")
	}
	c, err := s.courseRepo.CreateCourse(instructorID, name, subject, gradeLevel, academicYear)
	if err != nil {
		return nil, fmt.Errorf("service: CreateCourse: %w", err)
	}
	return c, nil
}

// ListCourses returns all courses owned by the instructor.
func (s *CourseService) ListCourses(instructorID int64) ([]models.Course, error) {
	return s.courseRepo.GetCoursesByInstructor(instructorID)
}

// GetCourse returns a single course; returns an error if it does not belong to
// the requesting instructor (unless the caller is an admin).
func (s *CourseService) GetCourse(courseID, callerID int64, isAdmin bool) (*models.Course, error) {
	c, err := s.courseRepo.GetCourseByID(courseID)
	if err != nil {
		return nil, fmt.Errorf("service: GetCourse: %w", err)
	}
	if !isAdmin && c.InstructorID != callerID {
		return nil, errors.New("service: GetCourse: not authorized")
	}
	return c, nil
}

// AddSection appends a section to the given course.
func (s *CourseService) AddSection(courseID int64, name, period, room string) (*models.CourseSection, error) {
	if name == "" {
		return nil, errors.New("service: AddSection: section name is required")
	}
	return s.courseRepo.AddSection(courseID, name, period, room)
}

// GetSections returns all sections for a course.
func (s *CourseService) GetSections(courseID int64) ([]models.CourseSection, error) {
	return s.courseRepo.GetSectionsByCourse(courseID)
}

// AddPlanItem adds (or updates) a material demand line for a course.
// sectionID is optional: pass nil to create a whole-course plan item.
// Verifies the material exists and is active before recording the plan.
func (s *CourseService) AddPlanItem(courseID, materialID int64, sectionID *int64, requestedQty int, notes string) (*models.CoursePlan, error) {
	if requestedQty <= 0 {
		return nil, errors.New("service: AddPlanItem: requested_qty must be positive")
	}
	mat, err := s.materialRepo.GetByID(materialID)
	if err != nil {
		return nil, fmt.Errorf("service: AddPlanItem: material %d not found", materialID)
	}
	if mat.Status != "active" {
		return nil, fmt.Errorf("service: AddPlanItem: material %q is not active", mat.Title)
	}
	return s.courseRepo.UpsertPlanItem(courseID, materialID, sectionID, requestedQty, notes)
}

// ApprovePlanItem approves a plan item (admin/instructor workflow).
func (s *CourseService) ApprovePlanItem(planID int64, approvedQty int) error {
	if approvedQty < 0 {
		return errors.New("service: ApprovePlanItem: approved_qty cannot be negative")
	}
	return s.courseRepo.ApprovePlanItem(planID, approvedQty)
}

// GetPlanItems returns all material demand lines for a course.
func (s *CourseService) GetPlanItems(courseID int64) ([]models.CoursePlan, error) {
	return s.courseRepo.GetPlansByCourse(courseID)
}
