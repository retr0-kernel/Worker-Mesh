package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkerNode holds the schema definition for the WorkerNode entity.
type WorkerNode struct {
	ent.Schema
}

// Fields of the WorkerNode.
func (WorkerNode) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique(),
		field.String("address"),
		field.String("status").Default("UNKNOWN"), // Changed to string
		field.Time("last_heartbeat").Default(time.Now),
		field.JSON("metadata", map[string]string{}).Optional(),
		field.String("version").Default("1.0.0"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the WorkerNode.
func (WorkerNode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("node_id").Unique(),
		index.Fields("status"),
	}
}
