package session

import (
	"context"
	"slices"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/card"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

// The verbs that act on an agent (lane verdicts; parent design §Verdict
// handling). Each calls herdr with the session's ctx, so once the session has
// closed, a verb in flight makes no further herdr call: its send_keys never
// goes out unless it had already started, and nothing is queued or replayed
// (parent 8.7). A call already under way runs to its reply or its 2 s
// timeout, since ctx only bounds the dial.

// show answers a show with the agent's card, built from a fresh agent.get and
// agent.read, or with a result's error.
func (s *Session) show(ctx context.Context, m *protocol.Show) protocol.Message {
	c, _, err := s.card(ctx, m.Pane)
	if err != nil {
		return s.failed(ctx, m.ID, "show", m.Pane, err)
	}
	return &protocol.Card{
		ID: m.ID, Pane: c.Pane, St: c.St, Seq: c.Seq, Hash: c.Hash, Kind: c.Kind, Risk: c.Risk, RL: c.Label,
		Summary: c.Summary, Lines: c.Lines, Can: c.Can,
	}
}

// approve and deny re-read the agent and its card, check the status, seq and
// hash, and only then send the keymap's keys, under the pane's lock.
func (s *Session) approve(ctx context.Context, m *protocol.Approve) *protocol.Result {
	return s.verdict(ctx, m.ID, "approve", m.Pane, m.Seq, m.Hash)
}

func (s *Session) deny(ctx context.Context, m *protocol.Deny) *protocol.Result {
	return s.verdict(ctx, m.ID, "deny", m.Pane, m.Seq, m.Hash)
}

// interrupt sends the keymap's interrupt keys to a working agent only, at
// the seq the device saw (parent 9.5, as phase 2's R6.3 narrows it): on a
// blocked prompt the interrupt key is a deny that would skip the card's
// checks. It re-reads the agent under the pane's lock, as a verdict does. A
// kind with no interrupt keys is not allowed one.
func (s *Session) interrupt(ctx context.Context, m *protocol.Interrupt) *protocol.Result {
	const verb = "interrupt"
	unlock, ok := s.deps.Locks.TryLock(m.Pane)
	if !ok {
		return s.stale(m.ID, verb, m.Pane, "in_flight")
	}
	defer unlock()
	info, err := s.deps.Herdr.GetAgent(ctx, m.Pane)
	if err != nil {
		return s.failed(ctx, m.ID, verb, m.Pane, err)
	}
	keys := s.deps.Keymaps.For(info.Agent).Keys(verb)
	switch {
	case len(keys) == 0:
		return s.notAllowed(m.ID, verb, m.Pane)
	case info.Status != "working":
		return s.stale(m.ID, verb, m.Pane, "status")
	case info.StateChangeSeq != m.Seq:
		return s.stale(m.ID, verb, m.Pane, "seq")
	}
	return s.sendKeys(ctx, m.ID, verb, m.Pane, info.StateChangeSeq, keys)
}

// jump calls agent.focus for the pane, and nothing else (parent 9.4). It
// needs no keymap.
func (s *Session) jump(ctx context.Context, m *protocol.Jump) *protocol.Result {
	if err := s.deps.Herdr.Focus(ctx, m.Pane); err != nil {
		return s.failed(ctx, m.ID, "jump", m.Pane, err)
	}
	s.deps.Log.Info("session: focused", "verb", "jump", "pane", m.Pane)
	return &protocol.Result{ID: m.ID, OK: true}
}

// verdict answers request id, a verdict (verb) on pane's card at seq with
// hash. It re-reads the agent and its card as show does. An agent whose kind
// has no keymap keys for verb is not allowed one (parent 8.6). Only if the
// agent is still blocked, at seq, on a permission prompt (never a question:
// parent 7.7), and its card still hashes to hash does it send the keymap's
// keys for verb, and only those (parent 8.3–8.5); else the verdict is stale,
// and nothing is sent. A verdict the card doesn't offer, an approve on a card
// that failed closed (the owner's decision, 2026-11-01), is not allowed. It
// holds pane's lock throughout, and a verdict that finds another one holding
// it is stale at once: verdicts are never queued (parent design §Threat
// model, T6).
func (s *Session) verdict(ctx context.Context, id uint32, verb, pane string, seq uint64, hash string) *protocol.Result {
	unlock, ok := s.deps.Locks.TryLock(pane)
	if !ok {
		return s.stale(id, verb, pane, "in_flight")
	}
	defer unlock()
	c, km, err := s.card(ctx, pane)
	if err != nil {
		return s.failed(ctx, id, verb, pane, err)
	}
	keys := km.Keys(verb)
	switch {
	case len(keys) == 0:
		return s.notAllowed(id, verb, pane)
	case c.St != "blocked":
		return s.stale(id, verb, pane, "status")
	case c.Seq != seq:
		return s.stale(id, verb, pane, "seq")
	case c.Kind != "permission":
		return s.stale(id, verb, pane, "kind")
	case c.Hash != hash:
		return s.stale(id, verb, pane, "hash")
	case !slices.Contains(c.Can, verb):
		return s.notAllowed(id, verb, pane)
	}
	return s.sendKeys(ctx, id, verb, pane, c.Seq, keys)
}

// sendKeys sends pane keys, the keymap's for verb, once: the last step of a
// verb whose checks passed at seq, under pane's lock. If keys already went
// to pane at seq, the agent hasn't moved on since, so the verb is stale and
// nothing is sent: a card is answered once. It answers request id `ok`, or
// the protocol's error for herdr's, and never retries (parent 8.5). herdr's
// `agent_not_ready`, the pane no longer being that agent, is `stale` (parent
// 8.4; the owner's decision, 2026-10-27, open-items O-46): herdr sent
// nothing, and the pager asks for the card as it is now.
func (s *Session) sendKeys(ctx context.Context, id uint32, verb, pane string, seq uint64, keys []string) *protocol.Result {
	if s.deps.Locks.answeredAt(pane, seq) {
		return s.stale(id, verb, pane, "answered")
	}
	if err := s.deps.Herdr.SendKeys(ctx, pane, keys); err != nil {
		if ctx.Err() == nil && herdr.Code(err) == "agent_not_ready" {
			return s.stale(id, verb, pane, "agent_not_ready")
		}
		return s.failed(ctx, id, verb, pane, err)
	}
	s.deps.Locks.answer(pane, seq)
	s.deps.Log.Info("session: "+noun(verb)+" sent", "verb", verb, "pane", pane, "seq", seq)
	return &protocol.Result{ID: id, OK: true}
}

// noun is what the log calls verb's request: an interrupt is one, and
// approve and deny are verdicts, which is what the day's records count (the
// owner's decision, 2026-10-27, open-items O-17).
func noun(verb string) string {
	if verb == "interrupt" {
		return verb
	}
	return "verdict"
}

// notAllowed answers request id, a verb on pane whose agent's kind has no
// keymap keys for it (parent 8.6), or whose card doesn't offer it (an approve
// on a card that failed closed), `not_allowed`, and logs the verb and the
// pane, never the kind.
func (s *Session) notAllowed(id uint32, verb, pane string) *protocol.Result {
	s.deps.Log.Info("session: the verb isn't offered; not allowed", "verb", verb, "pane", pane)
	return &protocol.Result{ID: id, OK: false, Err: "not_allowed"}
}

// stale answers request id, a verb on pane that no longer applies (parent
// 8.4), and logs why: what changed (status, seq, kind or hash), in_flight
// for another verb on the pane, answered for keys already sent at its seq,
// or agent_not_ready for herdr's refusal of the send.
func (s *Session) stale(id uint32, verb, pane, why string) *protocol.Result {
	s.deps.Log.Info("session: "+noun(verb)+" stale; nothing sent", "verb", verb, "pane", pane, "why", why)
	return &protocol.Result{ID: id, Err: "stale"}
}

// card reads pane's agent and its detection text from herdr, now, and builds
// its card with its kind's keymap, which it returns too (nil for a kind
// without one).
func (s *Session) card(ctx context.Context, pane string) (card.Card, *keymap.Map, error) {
	info, err := s.deps.Herdr.GetAgent(ctx, pane)
	if err != nil {
		return card.Card{}, nil, err
	}
	text, err := s.deps.Herdr.ReadDetection(ctx, pane)
	if err != nil {
		return card.Card{}, nil, err
	}
	km := s.deps.Keymaps.For(info.Agent)
	return card.BuildFor(info, text, km, s.deps.Rules, s.rows), km, nil
}

// failed answers request id, whose herdr call for pane failed with err, with
// the protocol's error for it, and logs the verb, the pane and herdr's code:
// never herdr's message, which can quote a pane's target. A call that failed
// because the session ended (ctx done) is logged as that, not as herdr's
// failure: nothing was sent, and no device is there to answer.
func (s *Session) failed(ctx context.Context, id uint32, verb, pane string, err error) *protocol.Result {
	code := herdr.Code(err)
	if ctx.Err() != nil {
		s.deps.Log.Info("session: closed; nothing sent", "verb", verb, "pane", pane)
	} else {
		s.deps.Log.Warn("session: herdr call failed", "verb", verb, "pane", pane, "code", code)
	}
	return &protocol.Result{ID: id, Err: errorOf(code)}
}

// errorOf is the protocol's error for herdr's code (parent design §Bridge
// components, herdr 0.9.3's socket): a pane herdr doesn't know is
// `unknown_pane`; herdr unavailable, its socket missing, refused or out of
// reach, or no reply within 2 s is `herdr_offline`; anything else is
// `herdr_error`.
func errorOf(code string) string {
	switch code {
	case "agent_not_found":
		return "unknown_pane"
	case "server_unavailable", "socket_missing", "socket_refused", "socket_permission", "timeout", "unreachable":
		return "herdr_offline"
	}
	return "herdr_error"
}
