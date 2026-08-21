// Mochi world: Damage hooks
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// DamageState is written by the battle package and consumed by the aero, FCS,
// propulsion and mass loops here. Every field stores LOSS, not effectiveness,
// so the zero value is a pristine jet and a zero-filled decode needs no fixup.

package flight

type DamageState struct {
	Element []float64  // per-element loss 0..1, flattened across surfaces in order (nil = pristine)
	Jam     []float64  // per-channel restriction 0..1: 0 = free, 1 = frozen at current deflection (nil = free)
	Engine  [4]float64 // thrust loss 0..1 per engine
	Gear    [3]float64 // per-strut damage 0..1 (nose, left, right): softened spring and blown-tyre drag/pull as it grows; the leg folds past collapse (#78 — hard touchdowns and weapon hits both deal it)
	Leak    float64    // kg/s fuel loss
	Drag    float64    // added parasitic drag area, m²
	Shift   Vec3       // CG shift from lost structure
	Loss    float64    // lost structure mass, kg (shed panels)
	Stress  float64    // accumulated overstress exposure, g·s beyond limits (over-g, negative-g, overspeed)
}

// Copy returns a DamageState whose slices are the caller's own. A struct
// assignment of State shares Element and Jam, so a scratch model copied from a
// live jet writes through to it - any rollout must Copy before it steps.
func (d DamageState) Copy() DamageState {
	if d.Element != nil {
		d.Element = append([]float64(nil), d.Element...)
	}
	if d.Jam != nil {
		d.Jam = append([]float64(nil), d.Jam...)
	}
	return d
}

// Gear damage thresholds: above GearTyre the wheel is blown (rolling drag,
// pull, weak braking on that leg); above GearCollapse the strut folds and
// carries nothing — the belly skids inherit the jet.
const (
	GearTyre     = 0.3
	GearCollapse = 0.7
)

// Actuator channels for Jam, in encode order.
const (
	ChannelStabilatorLeft = iota
	ChannelStabilatorRight
	ChannelFlaperonLeft
	ChannelFlaperonRight
	ChannelRudder
	ChannelSlat
	ChannelSpeedbrake
	Channels = 8 // encoded budget (one reserved)
)

// Elements is the encoded per-element damage budget; New refuses airframes
// with more elements than the wire can carry.
const Elements = 40

func (d *DamageState) element(i int) float64 {
	if d.Element == nil || i >= len(d.Element) {
		return 1
	}
	return 1 - d.Element[i]
}

func (d *DamageState) engine(i int) float64 { return 1 - d.Engine[i] }

func (d *DamageState) jam(c int) float64 {
	if d.Jam == nil || c >= len(d.Jam) {
		return 1
	}
	return 1 - d.Jam[c]
}
