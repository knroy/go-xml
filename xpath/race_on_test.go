//go:build race

package xpath

// raceEnabled is true when the package is built with -race, whose
// instrumentation slows a compile several times over; timing tests scale
// their budgets by it rather than fail on the gate's race lane.
const raceEnabled = true
