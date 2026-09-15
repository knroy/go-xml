//go:build race

package xdm

// raceEnabled is true when the package is built with -race, whose
// instrumentation slows a parse several times over; timing tests scale their
// budgets by it rather than fail on the gate's race lane.
const raceEnabled = true
