package swarmsearch

import (
	"net"
	"strconv"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// handleSnPeers ingests an inbound sn_peers PEX frame (msg_type 9). It accepts
// gossip ONLY from a peer that advertised BitPeerGossip (the consent model — a
// peer that sends PEX without opting in is charged), decodes + IP-sanity-filters
// the addresses, and hands the dialable ones to the PeerSink (the engine, which
// introduces them to its rendezvous swarms).
func (p *Protocol) handleSnPeers(addr string, payload []byte) {
	if !p.peerHasGossip(addr) {
		p.ban.Add(addr, ScoreUnexpectedMessage)
		return
	}
	addrs, err := ltepwire.DecodeSnPeers(payload)
	if err != nil {
		p.ban.Add(addr, ScoreBadBencode)
		return
	}
	dial := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if s, ok := compactToDial(a); ok {
			dial = append(dial, s)
		}
	}
	p.mu.Lock()
	sink := p.peerSink
	p.mu.Unlock()
	if sink != nil && len(dial) > 0 {
		sink.AddDiscoveredPeers(dial)
	}
}

// peerHasGossip reports whether the peer advertised BitPeerGossip.
func (p *Protocol) peerHasGossip(addr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	ps, ok := p.peers[addr]
	return ok && ps.Services.Has(ltepwire.BitPeerGossip)
}

// gossipToAllLocked enqueues an sn_peers introduction to EVERY gossip-capable
// peer. Called (caller holds p.mu) when a peer newly advertises BitPeerGossip:
// the new peer learns the existing peers AND — crucially — the existing peers
// learn the new one. A one-shot introduction to just the new peer would starve
// early-arriving peers of every later arrival (they were gossiped an empty/small
// list at connect and never re-notified). Enqueue is non-blocking (safe under
// p.mu); a saturated queue drops and the next new-peer event re-broadcasts.
func (p *Protocol) gossipToAllLocked() {
	for _, ps := range p.peers {
		if !ps.Supported || !ps.Services.Has(ltepwire.BitPeerGossip) {
			continue
		}
		select {
		case p.announceCh <- announceReq{token: ps.token, gossip: true}:
		default:
			p.log.Debug("swarmsearch.gossip_dropped_queue_full", "addr", ps.Addr)
		}
	}
}

// sendGossip builds and sends an sn_peers frame to the token's peer: the
// addresses of OTHER peers that advertised BitPeerGossip (consent), excluding
// the recipient and any non-public address, capped at MaxPeersPerGossip.
func (p *Protocol) sendGossip(tok PeerToken) {
	recipient := tok.Addr()
	p.mu.Lock()
	t := p.transport
	addrs := make([]ltepwire.CompactAddr, 0, ltepwire.MaxPeersPerGossip)
	for a, ps := range p.peers {
		if a == recipient || !ps.Supported || !ps.Services.Has(ltepwire.BitPeerGossip) {
			continue
		}
		if ca, ok := addrToCompact(a); ok {
			addrs = append(addrs, ca)
			if len(addrs) >= ltepwire.MaxPeersPerGossip {
				break
			}
		}
	}
	p.mu.Unlock()
	if t == nil || !tok.valid() || len(addrs) == 0 {
		return
	}
	frame, err := ltepwire.EncodeSnPeers(addrs)
	if err != nil {
		p.log.Debug("swarmsearch.gossip_encode_err", "err", err)
		return
	}
	if err := t.SendExtension(tok, frame); err != nil {
		p.log.Debug("swarmsearch.gossip_send_err", "addr", recipient, "err", err)
	}
}

// addrToCompact parses an "ip:port" peer address into a compact endpoint,
// accepting only public numeric addresses (so we neither leak nor propagate
// loopback/private/link-local peers).
func addrToCompact(addr string) (ltepwire.CompactAddr, bool) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return ltepwire.CompactAddr{}, false
	}
	ip := net.ParseIP(host)
	if !isPublicIP(ip) {
		return ltepwire.CompactAddr{}, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return ltepwire.CompactAddr{}, false
	}
	if v4 := ip.To4(); v4 != nil {
		return ltepwire.CompactAddr{IP: v4, Port: uint16(port)}, true
	}
	return ltepwire.CompactAddr{IP: ip.To16(), Port: uint16(port)}, true
}

// compactToDial renders a received compact endpoint as an "ip:port" dial string,
// accepting only public addresses (SSRF guard — a hostile gossip cannot steer us
// at loopback/private/link-local hosts).
func compactToDial(a ltepwire.CompactAddr) (string, bool) {
	ip := net.IP(a.IP)
	if !isPublicIP(ip) || a.Port == 0 {
		return "", false
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(a.Port))), true
}

// isPublicIP reports whether ip is a routable public unicast address — rejecting
// unspecified, loopback, private, link-local, and multicast ranges.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	return true
}
