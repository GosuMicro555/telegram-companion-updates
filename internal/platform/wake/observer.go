// Package wake receives operating-system wake notifications.
package wake

// Observer receives wake notifications until it is stopped. Start and Stop are
// idempotent so application lifecycle hooks may safely call each once.
type Observer interface {
	Start()
	Stop()
}
