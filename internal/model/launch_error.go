package model

// LaunchError reports a workspace that was created before agent startup failed.
// PublicMessage must be safe to show to the user; Cause stays server-side.
type LaunchError struct {
	WorkspaceID   string
	PublicMessage string
	Cause         error
}

func (e *LaunchError) Error() string {
	if e == nil || e.PublicMessage == "" {
		return "Could not start the agent in the created workspace."
	}
	return e.PublicMessage
}
func (e *LaunchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
