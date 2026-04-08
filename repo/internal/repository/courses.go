package repository

import (
	"database/sql"
	"fmt"

	"w2t86/internal/models"
)

// CourseRepository provides database operations for courses, course sections,
// and course material demand plans.
type CourseRepository struct {
	db *sql.DB
}

// NewCourseRepository returns a CourseRepository backed by the given database.
func NewCourseRepository(db *sql.DB) *CourseRepository {
	return &CourseRepository{db: db}
}

// ---------------------------------------------------------------
// Courses
// ---------------------------------------------------------------

// CreateCourse inserts a new course row and returns the populated model.
func (r *CourseRepository) CreateCourse(instructorID int64, name, subject, gradeLevel, academicYear string) (*models.Course, error) {
	const q = `
		INSERT INTO courses (instructor_id, name, subject, grade_level, academic_year)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id, instructor_id, name, subject, grade_level, academic_year, created_at, updated_at`

	row := r.db.QueryRow(q, instructorID,
		nullStr(name), nullStr(subject), nullStr(gradeLevel), nullStr(academicYear))
	c := &models.Course{}
	if err := row.Scan(&c.ID, &c.InstructorID, &c.Name, &c.Subject,
		&c.GradeLevel, &c.AcademicYear, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("repository: CreateCourse: %w", err)
	}
	return c, nil
}

// GetCoursesByInstructor returns all courses owned by the given instructor.
func (r *CourseRepository) GetCoursesByInstructor(instructorID int64) ([]models.Course, error) {
	const q = `
		SELECT id, instructor_id, name, subject, grade_level, academic_year, created_at, updated_at
		FROM   courses
		WHERE  instructor_id = ?
		ORDER  BY name`

	rows, err := r.db.Query(q, instructorID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetCoursesByInstructor: %w", err)
	}
	defer rows.Close()
	return scanCourses(rows)
}

// GetCourseByID returns a single course by its primary key.
func (r *CourseRepository) GetCourseByID(id int64) (*models.Course, error) {
	const q = `
		SELECT id, instructor_id, name, subject, grade_level, academic_year, created_at, updated_at
		FROM   courses
		WHERE  id = ?`

	row := r.db.QueryRow(q, id)
	c := &models.Course{}
	if err := row.Scan(&c.ID, &c.InstructorID, &c.Name, &c.Subject,
		&c.GradeLevel, &c.AcademicYear, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("repository: GetCourseByID: course %d not found", id)
		}
		return nil, fmt.Errorf("repository: GetCourseByID: %w", err)
	}
	return c, nil
}

// ---------------------------------------------------------------
// Course Sections
// ---------------------------------------------------------------

// AddSection inserts a new section for a course.
func (r *CourseRepository) AddSection(courseID int64, name, period, room string) (*models.CourseSection, error) {
	const q = `
		INSERT INTO course_sections (course_id, name, period, room)
		VALUES (?, ?, ?, ?)
		RETURNING id, course_id, name, period, room, created_at`

	row := r.db.QueryRow(q, courseID, name, nullStr(period), nullStr(room))
	s := &models.CourseSection{}
	if err := row.Scan(&s.ID, &s.CourseID, &s.Name, &s.Period, &s.Room, &s.CreatedAt); err != nil {
		return nil, fmt.Errorf("repository: AddSection: %w", err)
	}
	return s, nil
}

// GetSectionsByCourse returns all sections for a course.
func (r *CourseRepository) GetSectionsByCourse(courseID int64) ([]models.CourseSection, error) {
	const q = `
		SELECT id, course_id, name, period, room, created_at
		FROM   course_sections
		WHERE  course_id = ?
		ORDER  BY name`

	rows, err := r.db.Query(q, courseID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetSectionsByCourse: %w", err)
	}
	defer rows.Close()

	var out []models.CourseSection
	for rows.Next() {
		var s models.CourseSection
		if err := rows.Scan(&s.ID, &s.CourseID, &s.Name, &s.Period, &s.Room, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository: GetSectionsByCourse: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// Course Plans
// ---------------------------------------------------------------

// UpsertPlanItem inserts or updates a material demand line for a course.
// sectionID may be nil to indicate a whole-course plan with no section scoping.
// On conflict (same course, section, and material) the row is updated in place.
func (r *CourseRepository) UpsertPlanItem(courseID, materialID int64, sectionID *int64, requestedQty int, notes string) (*models.CoursePlan, error) {
	const q = `
		INSERT INTO course_plans (course_id, section_id, material_id, requested_qty, notes, status)
		VALUES (?, ?, ?, ?, ?, 'pending')
		ON CONFLICT DO UPDATE SET
			requested_qty = excluded.requested_qty,
			notes         = excluded.notes,
			updated_at    = datetime('now')
		RETURNING id, course_id, section_id, material_id, requested_qty, approved_qty, status, notes, created_at, updated_at`

	row := r.db.QueryRow(q, courseID, sectionID, materialID, requestedQty, nullStr(notes))
	return scanCoursePlan(row)
}

// ApprovePlanItem sets approved_qty and transitions status to 'approved'.
func (r *CourseRepository) ApprovePlanItem(planID int64, approvedQty int) error {
	const q = `
		UPDATE course_plans
		SET    approved_qty = ?, status = 'approved', updated_at = datetime('now')
		WHERE  id = ? AND status = 'pending'`
	_, err := r.db.Exec(q, approvedQty, planID)
	return err
}

// GetPlansByCourse returns all demand plan items for a course, including the
// section name when a section is assigned.
func (r *CourseRepository) GetPlansByCourse(courseID int64) ([]models.CoursePlan, error) {
	const q = `
		SELECT cp.id, cp.course_id, cp.section_id, cp.material_id,
		       cp.requested_qty, cp.approved_qty, cp.status, cp.notes,
		       cp.created_at, cp.updated_at,
		       cs.name
		FROM   course_plans cp
		LEFT   JOIN course_sections cs ON cs.id = cp.section_id
		WHERE  cp.course_id = ?
		ORDER  BY COALESCE(cs.name, ''), cp.created_at`

	rows, err := r.db.Query(q, courseID)
	if err != nil {
		return nil, fmt.Errorf("repository: GetPlansByCourse: %w", err)
	}
	defer rows.Close()

	var out []models.CoursePlan
	for rows.Next() {
		var p models.CoursePlan
		if err := rows.Scan(&p.ID, &p.CourseID, &p.SectionID, &p.MaterialID,
			&p.RequestedQty, &p.ApprovedQty, &p.Status, &p.Notes,
			&p.CreatedAt, &p.UpdatedAt, &p.SectionName); err != nil {
			return nil, fmt.Errorf("repository: GetPlansByCourse: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func scanCourses(rows *sql.Rows) ([]models.Course, error) {
	var out []models.Course
	for rows.Next() {
		c := models.Course{}
		if err := rows.Scan(&c.ID, &c.InstructorID, &c.Name, &c.Subject,
			&c.GradeLevel, &c.AcademicYear, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanCourses: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanCoursePlan(row *sql.Row) (*models.CoursePlan, error) {
	p := &models.CoursePlan{}
	if err := row.Scan(&p.ID, &p.CourseID, &p.SectionID, &p.MaterialID,
		&p.RequestedQty, &p.ApprovedQty, &p.Status, &p.Notes,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("repository: scanCoursePlan: %w", err)
	}
	return p, nil
}
