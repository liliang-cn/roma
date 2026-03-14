package tododemo

const healthPayload = "todo-webapp-ok"

// HealthMessage returns the fixed payload for the TODO demo health probe.
func HealthMessage() string {
	return healthPayload
}
