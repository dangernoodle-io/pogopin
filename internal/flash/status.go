package flash

// StatusFunc reports a transport-neutral, phase-labeled status tick for a
// flash_external run. It mirrors esp.StatusFunc's shape and nil-safety
// contract exactly (func(phase string, current, total int), nil callers
// opt out silently) but is declared independently here rather than imported
// from internal/esp, since internal/flash has no dependency on internal/esp
// and shouldn't gain one just for a status callback type.
type StatusFunc func(phase string, current, total int)

// Status phase names emitted through StatusFunc, in the order Flash() emits
// them. Mid-command byte progress (inside the opaque cmd.Run()) is out of
// scope here — it needs streamed stdout, a bigger follow-up redesign — so
// every phase below is a coarse marker with current=total=0, not a bar.
const (
	StatusPhaseStoppingPort  = "stopping port"
	StatusPhaseRunningCmd    = "running command"
	StatusPhaseRestarting    = "restarting"
	StatusPhaseCapturingBoot = "capturing boot"
	StatusPhaseComplete      = "complete"
)

// emitStatus is a nil-safe StatusFunc invocation helper for a discrete
// phase-transition tick with no byte denominator (current=0, total=0),
// mirroring internal/esp's emitStatus helper.
func emitStatus(status StatusFunc, phase string) {
	if status == nil {
		return
	}
	status(phase, 0, 0)
}
