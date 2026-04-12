// Package testlab is an in-process multi-node test harness for
// SwartzNet — Layer A of the test architecture in
// docs/11-testbed-architecture.md (Layer A is "N engines in one
// Go process, real sockets, no containers").
//
// The goal is to let tests exercise the Layer-L local index,
// Layer-S sn_search peer-wire protocol, and Layer-D DHT keyword
// index with more than one node without spinning up containers
// or hitting the live mainline DHT. Every node is a real
// *engine.Engine with its own state directories and ephemeral
// TCP/uTP listen ports; they connect to each other over
// loopback and advertise the sn_search extension through the
// normal LTEP handshake path.
//
// Usage sketch:
//
//	func TestMyScenario(t *testing.T) {
//	    c := testlab.NewCluster(t, 3)
//	    defer c.Stop()
//
//	    // Node 0 seeds some content into its local index.
//	    c.Nodes[0].IndexTorrent("ubuntu", "...body...")
//
//	    // Put every node in a shared dummy swarm so LTEP
//	    // handshakes fire and sn_search is advertised.
//	    c.WireMesh(t)
//	    c.WaitAllHandshaked(t, 5*time.Second)
//
//	    // Node 2 queries the swarm and should see node 0's hit.
//	    hits := c.Nodes[2].SwarmQuery(t, "ubuntu")
//	    if len(hits) == 0 {
//	        t.Fatal("no swarm hits")
//	    }
//	}
//
// This package is only useful from _test.go files; it opens
// real sockets, writes to t.TempDir() directories, and assumes
// a *testing.T is available for cleanup hooks.
package testlab
