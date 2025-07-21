package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Job holds the schema definition for the Job entity.
type Job struct {
	ent.Schema
}

// Fields of the Job.
func (Job) Fields() []ent.Field {
	return []ent.Field{
		field.String("job_id").Unique(),
		field.String("target_node_id"),
		field.String("command"),
		field.JSON("env", map[string]string{}).Optional(),
		field.Int32("timeout_seconds").Default(300),
		field.String("status").Default("JOB_PENDING"), // Changed to string
		field.String("created_by_node"),
		field.Int32("exit_code").Optional(),
		field.Text("stdout").Optional(),
		field.Text("stderr").Optional(),
		field.Text("error_message").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("started_at").Optional(),
		field.Time("finished_at").Optional(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the Job.
func (Job) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("job_id").Unique(),
		index.Fields("status"),
		index.Fields("target_node_id"),
	}
}
