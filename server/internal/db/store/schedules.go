package store

import (
	"context"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Schedule is a recurring task template (docs/api.md "Schedules").
type Schedule struct {
	ExecutionConfig *execution.Config `json:"executionConfig"`
	ConnectionID    int64             `json:"connectionId"`
	BindingID       *int64            `json:"bindingId"`
	Workspace       *TaskWorkspace    `json:"-"`
	OriginSession   *SessionRef       `json:"originSession"`
	ID              int64             `json:"id"`
	ProjectID       int64             `json:"projectId"`
	ProjectTitle    string            `json:"projectTitle"`
	Title           string            `json:"title"`
	Prompt          string            `json:"prompt"`
	Notes           string            `json:"notes"`
	Attachments     []Attachment      `json:"attachments"`
	Cron            string            `json:"cron"`
	Tz              string            `json:"tz"`
	Paused          bool              `json:"paused"`
	NextRunAt       *time.Time        `json:"nextRunAt"`
	LastRunAt       *time.Time        `json:"lastRunAt"`
	LastTaskID      *int64            `json:"lastTaskId"`
	LastTaskState   *TaskState        `json:"lastTaskState"`
	CreatedAt       time.Time         `json:"createdAt"`
	CreatedBy       int64             `json:"createdBy"` // user who created it; spawned tasks inherit it
	// SourceMachineID: the machine whose agent gateway forwarded the create
	// (nil: not through a gateway). Spawned tasks do not inherit it.
	SourceMachineID *int64    `json:"sourceMachineId"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// ScheduleRun is one fire of a schedule.
type ScheduleRun struct {
	Attribution
	ID         int64     `json:"id"`
	ScheduleID int64     `json:"scheduleId"`
	FireAt     time.Time `json:"fireAt"`
	Outcome    string    `json:"outcome"`
	TaskID     *int64    `json:"taskId"`
	Detail     string    `json:"detail"`
	At         time.Time `json:"at"`
	Task       *Task     `json:"-"` // spawned task (detail reads)
}

// ScheduleFilter: ProjectID; DueBefore = unpaused with next_run_at <= it.
type ScheduleFilter struct {
	ProjectID *int64
	DueBefore *time.Time
}

// DueTask is a backlog task whose one-shot start has come.
type DueTask struct {
	ID      int64
	StartAt time.Time
}

// ScheduleInsert is a new schedule's columns.
type ScheduleInsert struct {
	ProjectID       int64
	BindingID       *int64
	Title           string
	Prompt          string
	Notes           string
	Cron            string
	Tz              string
	Paused          bool
	NextRunAt       *time.Time
	OriginSessionID *int64
	ExecutionConfig *execution.Config
	ConnectionID    int64
	CreatedBy       int64
	SourceMachineID *int64
}

// ScheduleEditState is what an edit checks before updating a schedule.
type ScheduleEditState struct {
	ExecutionConfig *execution.Config
	Cron            string
	Tz              string
	Paused          bool
	ConnectionID    int64
	ProjectID       int64
	BindingID       *int64
}

// ScheduleUpdate is the columns an edit sets (unset fields are kept;
// ExecutionConfig and NextRunAt Null clears).
type ScheduleUpdate struct {
	ConnectionID    Optional[int64]
	BindingID       Optional[int64]
	ExecutionConfig Optional[execution.Config]
	ProjectID       Optional[int64]
	Title           Optional[string]
	Prompt          Optional[string]
	Notes           Optional[string]
	Cron            Optional[string]
	Tz              Optional[string]
	Paused          Optional[bool]
	NextRunAt       Optional[time.Time]
}

// ScheduleTemplate is what a schedule spawns a task from.
type ScheduleTemplate struct {
	ProjectID       int64
	BindingID       *int64
	Title           string
	Prompt          string
	Notes           string
	Tz              string
	ConnectionID    int64
	OriginSession   *SessionRef
	ExecutionConfig *execution.Config
	CreatedBy       int64
}

// ScheduleFireState is what a fire reads.
type ScheduleFireState struct {
	Cron      string
	Tz        string
	Paused    bool
	NextRunAt *time.Time
}

// Schedules is the recurring schedule repository.
type Schedules interface {
	// Lock locks the schedule rows id until the unit of work ends, on engines
	// with row locks (Postgres FOR NO KEY UPDATE, ascending id; a missing row
	// is skipped). SQLite's single writer already serializes writes: no-op.
	// Take locks in the order of docs/storage.md "Transactions and locking":
	// machine, connection, tree, task, schedule.
	Lock(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (Schedule, error)
	// List orders by next fire (paused last), then id.
	List(ctx context.Context, f ScheduleFilter) ([]Schedule, error)
	Exists(ctx context.Context, id int64) (bool, error)
	Insert(ctx context.Context, in ScheduleInsert) (int64, error)
	EditState(ctx context.Context, id int64) (ScheduleEditState, error)
	// Update sets u's columns and updated_at.
	Update(ctx context.Context, id int64, u ScheduleUpdate) error
	// Delete removes the schedule, its runs and images (false: none).
	Delete(ctx context.Context, id int64) (bool, error)
	// Runs are the newest limit runs.
	Runs(ctx context.Context, id int64, limit int) ([]ScheduleRun, error)
	Template(ctx context.Context, id int64) (ScheduleTemplate, error)
	InsertRun(ctx context.Context, id int64, fireAt time.Time, outcome string, taskID *int64, detail string, attribution Attribution) error
	// Due: unpaused schedules (of connection; 0 = any) due at now.
	Due(ctx context.Context, now time.Time, connection int64) ([]int64, error)
	// DueTasks: open backlog tasks (of connection; 0 = any) whose start
	// time has come, oldest first.
	DueTasks(ctx context.Context, now time.Time, connection int64) ([]DueTask, error)
	FireState(ctx context.Context, id int64) (ScheduleFireState, error)
	// PendingTask is the newest spawned task still pending or active
	// (ErrNotFound: none).
	PendingTask(ctx context.Context, id int64) (int64, TaskState, error)
	SetNextRun(ctx context.Context, id int64, next *time.Time) error
}
