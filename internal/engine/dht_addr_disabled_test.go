package engine_test

import "testing"

// TestDHTAddrReturnsNilWhenDHTDisabled covers DHTAddr's
// `srv := e.dhtServer(); if srv == nil { return nil }` arm.
// newTestEngine uses DisableDHT=true so the embedded server is
// nil and DHTAddr must return nil rather than dereference the
// zero server.
func TestDHTAddrReturnsNilWhenDHTDisabled(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	if got := eng.DHTAddr(); got != nil {
		t.Errorf("DHTAddr() = %v, want nil with DHT disabled", got)
	}
}
