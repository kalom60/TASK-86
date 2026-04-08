package models

// models.go — Go structs for every table in migrations/001_schema.sql.
//
// Conventions:
//   - All IDs are int64 (maps to SQLite INTEGER PRIMARY KEY).
//   - Nullable TEXT columns → *string.
//   - Nullable REAL / INTEGER columns that may be NULL in the DB → *float64 / *int.
//   - Timestamp columns are stored as ISO-8601 TEXT; represented as *string so
//     that NULL is preserved without importing time in every layer.
//   - db struct tags match column names exactly.

// ---------------------------------------------------------------
// Users & Auth
// ---------------------------------------------------------------

// User represents a row in the users table.
type User struct {
	ID             int64   `db:"id"`
	Username       string  `db:"username"`
	Email          string  `db:"email"`
	PasswordHash   string  `db:"password_hash"`
	Role           string  `db:"role"`
	FailedAttempts int     `db:"failed_attempts"`
	LockedUntil    *string `db:"locked_until"`
	DateOfBirth    *string `db:"date_of_birth"`
	FullName       *string `db:"full_name"`    // real name; used for duplicate detection
	ExternalID     *string `db:"external_id"`  // institution-issued ID (student/employee number)
	CreatedAt      string  `db:"created_at"`
	UpdatedAt      string  `db:"updated_at"`
	DeletedAt      *string `db:"deleted_at"`
}

// Session represents a row in the sessions table.
type Session struct {
	ID        int64  `db:"id"`
	UserID    int64  `db:"user_id"`
	TokenHash string `db:"token_hash"`
	ExpiresAt string `db:"expires_at"`
	CreatedAt string `db:"created_at"`
}

// UserCustomField represents a row in the user_custom_fields table.
type UserCustomField struct {
	ID          int64   `db:"id"`
	UserID      int64   `db:"user_id"`
	FieldName   string  `db:"field_name"`
	FieldValue  *string `db:"field_value"`
	IsEncrypted int     `db:"is_encrypted"`
}

// ---------------------------------------------------------------
// Materials
// ---------------------------------------------------------------

// Material represents a row in the materials table.
type Material struct {
	ID           int64   `db:"id"`
	ISBN         *string `db:"isbn"`
	Title        string  `db:"title"`
	Author       *string `db:"author"`
	Publisher    *string `db:"publisher"`
	Edition      *string `db:"edition"`
	Subject      *string `db:"subject"`
	GradeLevel   *string `db:"grade_level"`
	TotalQty     int     `db:"total_qty"`
	AvailableQty int     `db:"available_qty"`
	ReservedQty  int     `db:"reserved_qty"`
	Status       string  `db:"status"`
	CreatedAt    string  `db:"created_at"`
	UpdatedAt    string  `db:"updated_at"`
	DeletedAt    *string `db:"deleted_at"`
}

// MaterialVersion represents a row in the material_versions table.
type MaterialVersion struct {
	ID         int64   `db:"id"`
	MaterialID int64   `db:"material_id"`
	ChangedBy  *int64  `db:"changed_by"`
	ChangeData string  `db:"change_data"`
	ChangedAt  string  `db:"changed_at"`
}

// Rating represents a row in the ratings table.
type Rating struct {
	ID         int64  `db:"id"`
	MaterialID int64  `db:"material_id"`
	UserID     int64  `db:"user_id"`
	Stars      int    `db:"stars"`
	CreatedAt  string `db:"created_at"`
}

// Comment represents a row in the comments table.
type Comment struct {
	ID          int64  `db:"id"`
	MaterialID  int64  `db:"material_id"`
	UserID      int64  `db:"user_id"`
	Body        string `db:"body"`
	LinkCount   int    `db:"link_count"`
	Status      string `db:"status"`
	ReportCount int    `db:"report_count"`
	CreatedAt   string `db:"created_at"`
	UpdatedAt   string `db:"updated_at"`
}

// CommentReport represents a row in the comment_reports table.
type CommentReport struct {
	ID         int64   `db:"id"`
	CommentID  int64   `db:"comment_id"`
	ReportedBy int64   `db:"reported_by"`
	Reason     *string `db:"reason"`
	CreatedAt  string  `db:"created_at"`
}

// FavoritesList represents a row in the favorites_lists table.
type FavoritesList struct {
	ID             int64   `db:"id"`
	UserID         int64   `db:"user_id"`
	Name           string  `db:"name"`
	Visibility     string  `db:"visibility"`
	ShareToken     *string `db:"share_token"`
	ShareExpiresAt *string `db:"share_expires_at"`
	CreatedAt      string  `db:"created_at"`
}

// FavoritesItem represents a row in the favorites_items table.
type FavoritesItem struct {
	ID         int64  `db:"id"`
	ListID     int64  `db:"list_id"`
	MaterialID int64  `db:"material_id"`
	AddedAt    string `db:"added_at"`
}

// BrowseHistory represents a row in the browse_history table.
type BrowseHistory struct {
	ID         int64  `db:"id"`
	UserID     int64  `db:"user_id"`
	MaterialID int64  `db:"material_id"`
	VisitedAt  string `db:"visited_at"`
}

// HistoryItem is a denormalized view of browse_history joined with material title.
type HistoryItem struct {
	MaterialID    int64  `db:"material_id"`
	MaterialTitle string `db:"material_title"`
	VisitedAt     string `db:"visited_at"`
}

// ---------------------------------------------------------------
// Orders
// ---------------------------------------------------------------

// Order represents a row in the orders table.
type Order struct {
	ID          int64   `db:"id"`
	UserID      int64   `db:"user_id"`
	Status      string  `db:"status"`
	TotalAmount float64 `db:"total_amount"`
	AutoCloseAt *string `db:"auto_close_at"`
	CreatedAt   string  `db:"created_at"`
	UpdatedAt   string  `db:"updated_at"`
}

// OrderItem represents a row in the order_items table.
type OrderItem struct {
	ID                int64   `db:"id"`
	OrderID           int64   `db:"order_id"`
	MaterialID        int64   `db:"material_id"`
	Qty               int     `db:"qty"`
	UnitPrice         float64 `db:"unit_price"`
	FulfillmentStatus string  `db:"fulfillment_status"`
}

// OrderEvent represents a row in the order_events table.
type OrderEvent struct {
	ID         int64   `db:"id"`
	OrderID    int64   `db:"order_id"`
	FromStatus *string `db:"from_status"`
	ToStatus   string  `db:"to_status"`
	ActorID    *int64  `db:"actor_id"`
	Note       *string `db:"note"`
	CreatedAt  string  `db:"created_at"`
}

// Backorder represents a row in the backorders table.
type Backorder struct {
	ID          int64   `db:"id"`
	OrderItemID int64   `db:"order_item_id"`
	Qty         int     `db:"qty"`
	ResolvedAt  *string `db:"resolved_at"`
	ResolvedBy  *int64  `db:"resolved_by"`
}

// ReturnRequest represents a row in the return_requests table.
type ReturnRequest struct {
	ID                    int64   `db:"id"`
	OrderID               int64   `db:"order_id"`
	UserID                int64   `db:"user_id"`
	Type                  string  `db:"type"`
	Status                string  `db:"status"`
	Reason                *string `db:"reason"`
	ReplacementMaterialID *int64  `db:"replacement_material_id"`
	RequestedAt           string  `db:"requested_at"`
	ResolvedAt            *string `db:"resolved_at"`
	ResolvedBy            *int64  `db:"resolved_by"`
}

// ---------------------------------------------------------------
// Distribution
// ---------------------------------------------------------------

// DistributionEvent represents a row in the distribution_events table.
type DistributionEvent struct {
	ID          int64   `db:"id"`
	OrderID     *int64  `db:"order_id"`
	MaterialID  int64   `db:"material_id"`
	Qty         int     `db:"qty"`
	EventType   string  `db:"event_type"`
	ScanID      *string `db:"scan_id"`
	ActorID     *int64  `db:"actor_id"`
	CustodyFrom *string `db:"custody_from"`
	CustodyTo   *string `db:"custody_to"`
	OccurredAt  string  `db:"occurred_at"`
}

// ---------------------------------------------------------------
// Messaging
// ---------------------------------------------------------------

// Notification represents a row in the notifications table.
type Notification struct {
	ID          int64   `db:"id"`
	UserID      int64   `db:"user_id"`
	Type        string  `db:"type"`
	Title       string  `db:"title"`
	Body        *string `db:"body"`
	RefID       *int64  `db:"ref_id"`
	RefType     *string `db:"ref_type"`
	ReadAt      *string `db:"read_at"`
	DeliveredAt *string `db:"delivered_at"`
	CreatedAt   string  `db:"created_at"`
}

// Subscription represents a row in the subscriptions table.
type Subscription struct {
	ID        int64  `db:"id"`
	UserID    int64  `db:"user_id"`
	Topic     string `db:"topic"`
	Active    int    `db:"active"`
	CreatedAt string `db:"created_at"`
}

// DNDSetting represents a row in the dnd_settings table.
type DNDSetting struct {
	ID        int64  `db:"id"`
	UserID    int64  `db:"user_id"`
	StartHour int    `db:"start_hour"`
	EndHour   int    `db:"end_hour"`
	UpdatedAt string `db:"updated_at"`
}

// ---------------------------------------------------------------
// Spatial
// ---------------------------------------------------------------

// Location represents a row in the locations table.
type Location struct {
	ID         int64   `db:"id"`
	Name       string  `db:"name"`
	Type       *string `db:"type"`
	GeomWKT    *string `db:"geom_wkt"`
	Lat        *float64 `db:"lat"`
	Lng        *float64 `db:"lng"`
	Properties *string `db:"properties"`
	CreatedAt  string  `db:"created_at"`
}

// SpatialAggregate represents a row in the spatial_aggregates table.
type SpatialAggregate struct {
	ID         int64    `db:"id"`
	LayerType  string   `db:"layer_type"`
	CellKey    string   `db:"cell_key"`
	Metric     string   `db:"metric"`
	Value      *float64 `db:"value"`
	ComputedAt string   `db:"computed_at"`
}

// ---------------------------------------------------------------
// Course Planning
// ---------------------------------------------------------------

// Course represents a row in the courses table.
type Course struct {
	ID           int64   `db:"id"`
	InstructorID int64   `db:"instructor_id"`
	Name         string  `db:"name"`
	Subject      *string `db:"subject"`
	GradeLevel   *string `db:"grade_level"`
	AcademicYear *string `db:"academic_year"`
	CreatedAt    string  `db:"created_at"`
	UpdatedAt    string  `db:"updated_at"`
}

// CourseSection represents a row in the course_sections table.
type CourseSection struct {
	ID        int64   `db:"id"`
	CourseID  int64   `db:"course_id"`
	Name      string  `db:"name"`
	Period    *string `db:"period"`
	Room      *string `db:"room"`
	CreatedAt string  `db:"created_at"`
}

// CoursePlan represents a row in the course_plans table.
type CoursePlan struct {
	ID           int64   `db:"id"`
	CourseID     int64   `db:"course_id"`
	SectionID    *int64  `db:"section_id"`
	MaterialID   int64   `db:"material_id"`
	RequestedQty int     `db:"requested_qty"`
	ApprovedQty  int     `db:"approved_qty"`
	Status       string  `db:"status"`
	Notes        *string `db:"notes"`
	CreatedAt    string  `db:"created_at"`
	UpdatedAt    string  `db:"updated_at"`
	// SectionName is populated by joined queries; not stored in the DB.
	SectionName *string `db:"-"`
}

// ---------------------------------------------------------------
// KPIs / Audit
// ---------------------------------------------------------------

// KPISnapshot represents a row in the kpi_snapshots table.
type KPISnapshot struct {
	ID         int64    `db:"id"`
	MetricName string   `db:"metric_name"`
	Dimension  *string  `db:"dimension"`
	Value      *float64 `db:"value"`
	Period     *string  `db:"period"`
	ComputedAt string   `db:"computed_at"`
}

// AuditLog represents a row in the audit_log table.
type AuditLog struct {
	ID         int64   `db:"id"`
	ActorID    *int64  `db:"actor_id"`
	Action     string  `db:"action"`
	EntityType string  `db:"entity_type"`
	EntityID   *int64  `db:"entity_id"`
	BeforeData *string `db:"before_data"`
	AfterData  *string `db:"after_data"`
	IP         *string `db:"ip"`
	CreatedAt  string  `db:"created_at"`
}

// FinancialTransaction represents a row in the financial_transactions table.
type FinancialTransaction struct {
	ID              int64    `db:"id"`
	OrderID         *int64   `db:"order_id"`
	ReturnRequestID *int64   `db:"return_request_id"`
	Type            string   `db:"type"`
	Amount          float64  `db:"amount"`
	Status          string   `db:"status"`
	Reference       *string  `db:"reference"`
	Note            *string  `db:"note"`
	ActorID         *int64   `db:"actor_id"`
	CreatedAt       string   `db:"created_at"`
	UpdatedAt       string   `db:"updated_at"`
}

// EntityDuplicate represents a row in the entity_duplicates table.
type EntityDuplicate struct {
	ID          int64   `db:"id"`
	EntityType  string  `db:"entity_type"`
	PrimaryID   int64   `db:"primary_id"`
	DuplicateID int64   `db:"duplicate_id"`
	Status      string  `db:"status"`
	MergedBy    *int64  `db:"merged_by"`
	MergedAt    *string `db:"merged_at"`
}
