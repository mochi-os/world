// Mochi world: State serialisation
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// One fixed float64 layout serves the wire snapshot, the prediction ring,
// and the wasm boundary. Values pass through bit-exact. The per-element
// damage slices are not carried — they are nil until the damage model
// (#78) lands, which extends this layout and bumps Version.

package flight

// Size is the encoded state length in float64 words.
const Size = 50

// Encode writes the state into out (at least Size long) and returns Size.
func (s *State) Encode(out []float64) int {
	out[0], out[1], out[2] = s.Position.X, s.Position.Y, s.Position.Z
	out[3], out[4], out[5] = s.Velocity.X, s.Velocity.Y, s.Velocity.Z
	out[6], out[7], out[8], out[9] = s.Attitude.W, s.Attitude.X, s.Attitude.Y, s.Attitude.Z
	out[10], out[11], out[12] = s.Omega.X, s.Omega.Y, s.Omega.Z
	out[13] = s.Fuel
	out[14], out[15] = s.Engine[0].Spool, s.Engine[0].Reheat
	out[16], out[17] = s.Engine[1].Spool, s.Engine[1].Reheat
	f := &s.Fcs
	out[18], out[19] = f.Stabilator.Left, f.Stabilator.Right
	out[20], out[21] = f.Flaperon.Left, f.Flaperon.Right
	out[22], out[23], out[24], out[25] = f.Rudder, f.Slat, f.Flap, f.Speedbrake
	out[26], out[27], out[28], out[29] = f.Integral, f.Trim, f.Washout, f.Demand
	out[30] = f.Normal
	g := &s.Gear
	out[31] = g.Extension
	out[32] = float64(g.Catapult)
	out[33] = g.Stroke
	out[34] = float64(g.Wire)
	out[35] = bit(g.Wow)
	out[36] = float64(g.Contact)
	out[37] = bit(g.Touch.Occurred)
	out[38], out[39] = g.Touch.Sink, g.Touch.Bank
	out[40] = float64(g.Touch.Kind)
	d := &s.Damage
	out[41], out[42] = d.Engine[0], d.Engine[1]
	out[43], out[44] = d.Leak, d.Drag
	out[45], out[46], out[47] = d.Shift.X, d.Shift.Y, d.Shift.Z
	out[48] = d.Stress
	out[49] = s.Time
	return Size
}

// Decode reads a state written by Encode.
func Decode(in []float64) State {
	s := State{}
	s.Position = Vec3{in[0], in[1], in[2]}
	s.Velocity = Vec3{in[3], in[4], in[5]}
	s.Attitude = Quat{in[6], in[7], in[8], in[9]}
	s.Omega = Vec3{in[10], in[11], in[12]}
	s.Fuel = in[13]
	s.Engine[0] = EngineState{Spool: in[14], Reheat: in[15]}
	s.Engine[1] = EngineState{Spool: in[16], Reheat: in[17]}
	f := &s.Fcs
	f.Stabilator = Pair{in[18], in[19]}
	f.Flaperon = Pair{in[20], in[21]}
	f.Rudder, f.Slat, f.Flap, f.Speedbrake = in[22], in[23], in[24], in[25]
	f.Integral, f.Trim, f.Washout, f.Demand = in[26], in[27], in[28], in[29]
	f.Normal = in[30]
	g := &s.Gear
	g.Extension = in[31]
	g.Catapult = int(in[32])
	g.Stroke = in[33]
	g.Wire = int(in[34])
	g.Wow = in[35] != 0
	g.Contact = int(in[36])
	g.Touch.Occurred = in[37] != 0
	g.Touch.Sink, g.Touch.Bank = in[38], in[39]
	g.Touch.Kind = int(in[40])
	d := &s.Damage
	d.Engine[0], d.Engine[1] = in[41], in[42]
	d.Leak, d.Drag = in[43], in[44]
	d.Shift = Vec3{in[45], in[46], in[47]}
	d.Stress = in[48]
	s.Time = in[49]
	return s
}

func bit(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
