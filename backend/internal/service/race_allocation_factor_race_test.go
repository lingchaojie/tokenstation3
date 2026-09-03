//go:build race

package service

// The race detector instruments allocations, so byte-count assertions retain
// their ordinary-build ceiling while allowing the instrumentation overhead.
const testRaceAllocationFactor uint64 = 2
