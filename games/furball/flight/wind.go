// Mochi world: Wind and turbulence
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A layered wind field, every layer a pure function of (position, time,
// environment): stateless (nothing to snapshot or rewind), position
// correlated (every aircraft feels the same field), and identical native
// and wasm. Layers: surface mean wind sheared by the open-sea power law,
// Ekman veer with height, a winds-aloft strengthening, mesoscale variation
// from very long wavelength components, Dryden-shaped turbulence from a
// frozen field of sinusoids with low-altitude scaling, and the carrier
// burble astern of the island.

package flight

import (
	"math"
)

const (
	shear        = 0.11  // open-sea power-law exponent
	veer         = 0.26  // rad of Ekman rotation over the first kilometre
	aloft        = 0.6   // fractional wind increase from boundary layer to 10 km
	gusts        = 8     // sinusoid components per turbulence axis
	burble_reach = 800.0 // m astern of the island the burble extends
)

// mix is a SplitMix64-style integer mixer: the deterministic noise source.
func mix(seed uint64, index uint64) uint64 {
	z := seed + index*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// noise maps a mixed value to [0, 1).
func noise(seed uint64, index uint64) float64 {
	return float64(mix(seed, index)>>11) / float64(1<<53)
}

// wind evaluates the total air velocity at a world position and sim time.
func wind(position Vec3, time float64, env Environment, carrier *Carrier) Vec3 {
	h := math.Max(position.Y, 2)

	// Mean wind: power-law shear from the 10 m reference, Ekman veer, and a
	// winds-aloft strengthening saturating at 10 km.
	profile := math.Pow(h/10, shear) * (1 + aloft*math.Min(h/10000, 1))
	turn := veer * math.Min(h/1000, 1)
	sin, cos := math.Sin(turn), math.Cos(turn)
	mean := Vec3{
		X: (env.Wind.X*cos - env.Wind.Z*sin) * profile,
		Y: 0,
		Z: (env.Wind.X*sin + env.Wind.Z*cos) * profile,
	}

	// Mesoscale variation: three very long wavelength components so the
	// breeze differs measurably across the map.
	strength := env.Wind.Length()
	for k := uint64(0); k < 3; k++ {
		wavelength := 20000 + 30000*noise(env.Seed, 900+k)
		angle := 2 * math.Pi * noise(env.Seed, 910+k)
		dx, dz := math.Cos(angle), math.Sin(angle)
		phase := 2 * math.Pi * (position.X*dx + position.Z*dz) / wavelength
		factor := 0.15 * strength * math.Sin(phase+2*math.Pi*noise(env.Seed, 920+k))
		mean.X += dx * factor
		mean.Z += dz * factor
	}

	// Cloud convection: extra gust intensity inside convective cells, and
	// the thermal/sink structure beneath their bases (convection.go).
	chop, draft := convection(position, env.Cloud)
	mean.Y += draft

	// Turbulence: a frozen field of sinusoids per axis, wavelengths log
	// spaced, amplitudes Dryden-ish (energy toward the longer wavelengths),
	// intensity and scale reduced near the surface (MIL-8785C low-altitude
	// character), advected past the aircraft with the mean wind and time.
	if env.Turbulence > 0 || chop > 0 {
		scale := clamp(h, 50, 533)  // Dryden scale length
		low := clamp(h/300, 0.4, 1) // near-surface intensity knockdown
		sigma := env.Turbulence*low + chop
		// Energy per octave: Kolmogorov 2/3 rolloff below the scale length,
		// flat above it — long wavelengths carry the energy (the felt bumps
		// at 0.2-1.5 Hz), short ones only light fast texture. The original
		// 1/(1+k) weights were INVERTED (most energy at the shortest band —
		// a 3 Hz buzz at combat speed), unfelt until cloud chop became the
		// field's first consumer.
		total := 0.0
		for k := uint64(0); k < gusts; k++ {
			total += math.Min(math.Pow(2, (float64(k)-3)*2.0/3.0), 1)
		}
		var gust Vec3
		for k := uint64(0); k < gusts; k++ {
			wavelength := scale * math.Pow(2, float64(k)-3) // scale/8 .. 16·scale
			weight := math.Min(math.Pow(2, (float64(k)-3)*2.0/3.0), 1) / total
			amplitude := sigma * math.Sqrt(2*weight)
			for axis := uint64(0); axis < 3; axis++ {
				i := k*8 + axis
				angle := 2 * math.Pi * noise(env.Seed, i)
				dx, dz := math.Cos(angle), math.Sin(angle)
				speed := 0.5 * strength // frozen field drifts with half the mean
				phase := 2*math.Pi*((position.X+speed*time)*dx+(position.Z+speed*time)*dz)/wavelength + 2*math.Pi*noise(env.Seed, 100+i)
				v := amplitude * math.Sin(phase)
				switch axis {
				case 0:
					gust.X += v
				case 1:
					gust.Y += v * 0.7 // vertical gusts run weaker
				default:
					gust.Z += v
				}
			}
		}
		mean = mean.Add(gust)
	}

	// Carrier burble: a deficit plus downdraft pocket astern of the island,
	// scaled by wind over deck. Deterministic, only near the boat.
	if carrier != nil {
		deck := carrier.Position.Add(Vec3{X: math.Cos(carrier.Heading), Z: -math.Sin(carrier.Heading)}.Scale(carrier.Speed * time))
		dx := Shortest(deck.X, position.X, env.Wrap)
		dz := Shortest(deck.Z, position.Z, env.Wrap)
		forward := Vec3{X: math.Cos(carrier.Heading), Z: -math.Sin(carrier.Heading)}
		behind := -(dx*forward.X + dz*forward.Z) // + when astern
		lateral := math.Abs(dx*forward.Z - dz*forward.X)
		height := position.Y - deck.Y
		if behind > 0 && behind < burble_reach && lateral < 60 && height > -5 && height < 60 {
			over := forward.Scale(-carrier.Speed).Subtract(mean).Length() // wind over deck
			fade := (1 - behind/burble_reach) * (1 - lateral/60) * math.Exp(-height/25)
			mean = mean.Add(forward.Scale(0.3 * over * fade)) // deficit along the deck wind
			mean.Y -= 0.4 * over * fade                       // the sink in the groove
		}
	}
	return mean
}
