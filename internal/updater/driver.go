package updater

import "context"

// Driver isolates platform-specific update engines from the service. Implementations
// may use a native framework, while tests can use an in-memory fake.
type Driver interface {
	Check(context.Context) (Update, error)
	Download(context.Context, func(progress int)) error
	Install(context.Context) error
	Stop() error
}

// Update is the non-secret result of an update check.
type Update struct {
	Available bool
	Version   string
}
