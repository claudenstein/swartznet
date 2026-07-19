package daemon

import (
	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// This file makes controllerAdapter satisfy httpapi.CapabilitiesController,
// translating between httpapi's SharingPrefs DTO and the ltepwire.Sharing
// contract so httpapi never imports the contract (zero-import law). The engine
// owns the sharing state; the Publisher bit is a read-only runtime fact.

// Sharing returns the engine's current operator sharing prefs.
func (a *controllerAdapter) Sharing() httpapi.SharingPrefs {
	s := a.eng.Sharing()
	return httpapi.SharingPrefs{
		ShareLocal:  int(s.ShareLocal),
		FileHits:    s.FileHits,
		ContentHits: s.ContentHits,
	}
}

// SetSharing stores the (already merged + clamped) prefs on the engine.
func (a *controllerAdapter) SetSharing(p httpapi.SharingPrefs) {
	a.eng.SetSharing(ltepwire.Sharing{
		ShareLocal:  uint8(p.ShareLocal),
		FileHits:    p.FileHits,
		ContentHits: p.ContentHits,
	})
}

// Publisher reports the daemon-owned Publishing runtime fact (read-only).
func (a *controllerAdapter) Publisher() bool {
	return a.eng.RuntimeFacts().Publishing
}
