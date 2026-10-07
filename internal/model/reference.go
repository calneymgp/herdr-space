package model

import "context"

// RuntimeReference is the minimum local metadata needed to revalidate a
// proven agent conversation after the web service restarts.
type RuntimeReference struct {
	Session  Session
	Socket   string
	AgentRef string
}

type ReferenceStore interface {
	LoadRuntimeReferences(context.Context) ([]RuntimeReference, error)
	SaveRuntimeReferences(context.Context, []RuntimeReference) error
}
