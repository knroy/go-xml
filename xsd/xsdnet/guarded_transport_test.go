package xsdnet

import "testing"

// Every fetch used to build its own guarded Transport, so no connection was
// reused and each fetch left idle-connection goroutines behind.
func TestGuardedTransportShared(t *testing.T) {
	if guardedTransport() != guardedTransport() {
		t.Fatal("guardedTransport returned different Transports: fetches cannot reuse connections")
	}
}
