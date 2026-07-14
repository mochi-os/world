// Mochi world: State serialisation
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// One fixed float64 layout serves the wire snapshot, the prediction ring,
// and the wasm boundary. Values pass through bit-exact. Four engine slots
// are always carried (airframes declare 0..4; unused slots stay zero), as
// are the Elements damage budget and the Channels jam budget — damage
// semantics are zero-means-pristine, so a pristine jet costs nothing but
// zeros and decodes back to nil slices.

package flight

// Size is the encoded state length in float64 words.
const Size = 57 + Elements + Channels + 1 + 3 // 109: base state, per-element loss, per-channel jams, lost mass, per-strut gear damage

// Encode writes the state into out (at least Size long) and returns Size.
func (s *State) Encode(out []float64) int {
	out[0], out[1], out[2] = s.Position.X, s.Position.Y, s.Position.Z
	out[3], out[4], out[5] = s.Velocity.X, s.Velocity.Y, s.Velocity.Z
	out[6], out[7], out[8], out[9] = s.Attitude.W, s.Attitude.X, s.Attitude.Y, s.Attitude.Z
	out[10], out[11], out[12] = s.Omega.X, s.Omega.Y, s.Omega.Z
	out[13] = s.Fuel
	for i := 0; i < 4; i++ {
		out[14+2*i], out[15+2*i] = s.Engine[i].Spool, s.Engine[i].Reheat
	}
	f := &s.Fcs
	out[22], out[23] = f.Stabilator.Left, f.Stabilator.Right
	out[24], out[25] = f.Flaperon.Left, f.Flaperon.Right
	out[26], out[27], out[28], out[29] = f.Rudder, f.Slat, f.Flap, f.Speedbrake
	out[30], out[31], out[32], out[33] = f.Integral, f.Trim, f.Washout, f.Demand
	out[34] = f.Normal
	g := &s.Gear
	out[35] = g.Extension
	out[36] = float64(g.Catapult)
	out[37] = g.Stroke
	out[38] = float64(g.Wire)
	out[39] = bit(g.Wow)
	out[40] = float64(g.Contact)
	out[41] = bit(g.Touch.Occurred)
	out[42], out[43] = g.Touch.Sink, g.Touch.Bank
	out[44] = float64(g.Touch.Kind)
	d := &s.Damage
	out[45], out[46], out[47], out[48] = d.Engine[0], d.Engine[1], d.Engine[2], d.Engine[3]
	out[49], out[50] = d.Leak, d.Drag
	out[51], out[52], out[53] = d.Shift.X, d.Shift.Y, d.Shift.Z
	out[54] = d.Stress
	out[55] = s.Time
	out[56] = s.Fcs.Reference
	for i := 0; i < Elements; i++ {
		v := 0.0
		if i < len(d.Element) {
			v = d.Element[i]
		}
		out[57+i] = v
	}
	for c := 0; c < Channels; c++ {
		v := 0.0
		if c < len(d.Jam) {
			v = d.Jam[c]
		}
		out[57+Elements+c] = v
	}
	out[57+Elements+Channels] = d.Loss
	out[57+Elements+Channels+1], out[57+Elements+Channels+2], out[57+Elements+Channels+3] = d.Gear[0], d.Gear[1], d.Gear[2]
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
	for i := 0; i < 4; i++ {
		s.Engine[i] = EngineState{Spool: in[14+2*i], Reheat: in[15+2*i]}
	}
	f := &s.Fcs
	f.Stabilator = Pair{in[22], in[23]}
	f.Flaperon = Pair{in[24], in[25]}
	f.Rudder, f.Slat, f.Flap, f.Speedbrake = in[26], in[27], in[28], in[29]
	f.Integral, f.Trim, f.Washout, f.Demand = in[30], in[31], in[32], in[33]
	f.Normal = in[34]
	g := &s.Gear
	g.Extension = in[35]
	g.Catapult = int(in[36])
	g.Stroke = in[37]
	g.Wire = int(in[38])
	g.Wow = in[39] != 0
	g.Contact = int(in[40])
	g.Touch.Occurred = in[41] != 0
	g.Touch.Sink, g.Touch.Bank = in[42], in[43]
	g.Touch.Kind = int(in[44])
	d := &s.Damage
	d.Engine[0], d.Engine[1], d.Engine[2], d.Engine[3] = in[45], in[46], in[47], in[48]
	d.Leak, d.Drag = in[49], in[50]
	d.Shift = Vec3{in[51], in[52], in[53]}
	d.Stress = in[54]
	s.Time = in[55]
	s.Fcs.Reference = in[56]
	// Slices materialise only when damage exists: a pristine wire stays nil,
	// so encode(decode(x)) is stable and the common case allocates nothing.
	for i := 0; i < Elements; i++ {
		if in[57+i] != 0 {
			d.Element = make([]float64, Elements)
			for k := 0; k < Elements; k++ {
				d.Element[k] = in[57+k]
			}
			break
		}
	}
	for c := 0; c < Channels; c++ {
		if in[57+Elements+c] != 0 {
			d.Jam = make([]float64, Channels)
			for k := 0; k < Channels; k++ {
				d.Jam[k] = in[57+Elements+k]
			}
			break
		}
	}
	d.Loss = in[57+Elements+Channels]
	d.Gear[0], d.Gear[1], d.Gear[2] = in[57+Elements+Channels+1], in[57+Elements+Channels+2], in[57+Elements+Channels+3]
	return s
}

func bit(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
