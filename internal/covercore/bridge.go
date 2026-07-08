package covercore

// This file is the seam that lets the interpreter reach a Collector's Core
// WITHOUT re-exposing the record side (Hit / Seed) to hosts and WITHOUT an import
// cycle.
//
// The cycle it avoids: pkg/cover.Collector wraps a *Core (pkg/cover imports
// covercore), so covercore cannot import pkg/cover to unwrap a Collector. Instead
// pkg/cover registers a one-line accessor here at init; internal/interp then calls
// CoreOf, passing the *cover.Collector it received from the engine. Both the
// accessor and CoreOf live in this internal package, so a host outside the module
// can reach neither -- it only ever holds a *cover.Collector, whose methods are
// the report side alone. This mirrors the standard-library idiom of bridging two
// packages through a function value set in init to sidestep an import cycle.

// collectorCore unwraps a *cover.Collector (passed as any to avoid importing
// pkg/cover) to its underlying *Core. pkg/cover installs it in init via
// SetCollectorBridge; it is nil until then, but the only caller (the interpreter)
// runs long after package initialization, so the accessor is always present.
var collectorCore func(any) *Core

// SetCollectorBridge installs the Collector->Core accessor. pkg/cover calls it
// once from its init so CoreOf can unwrap the Collector the engine hands the
// interpreter. Calling it more than once simply reinstalls the same accessor.
func SetCollectorBridge(f func(any) *Core) { collectorCore = f }

// CoreOf returns the *Core behind a *cover.Collector, or nil when coll is nil, is
// not a *cover.Collector, or the bridge is uninstalled. The interpreter uses it to
// turn the host-facing Collector it gets from the engine into the internal Core it
// records hits and seeds on -- the one path from the public wrapper to the private
// record side, and one that only in-module (internal) callers can take.
func CoreOf(coll any) *Core {
	if collectorCore == nil || coll == nil {
		return nil
	}
	return collectorCore(coll)
}
