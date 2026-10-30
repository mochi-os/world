// Mochi world: Wind and turbulence
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// A layered wind field, every layer a pure function of (position, time,
// environment): stateless, position correlated (every aircraft feels the same
// field), and identical native and wasm.

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

// Wind is the air's own velocity at a point of the environment's frame and a
// session time, in that frame: the whole field, without a carrier's burble.
// The weapons fly through it - a missile, a shell, a flare and a falling wreck
// are carried by the air just as the jets are, so a shot flies the same
// whichever way the wind blows.
func Wind(position Vec3, time float64, env Environment) Vec3 {
	theatre := env.Theatre()
	place, level := theatre.Survey(position)
	air := field(env.Placement().Point(position), place.Altitude, time, env, nil, Identity)
	return Between(level, theatre).Vector(air)
}

// field is the air's velocity at a point, in the tangent frame under it (x
// east, y up, z south): home is the point in Home, where the weather's pattern
// is fixed so every host meets the same air, and height its altitude. The
// carrier's burble is added when a carrier is given, place carrying the
// caller's level frame to Home.
func field(home Vec3, height float64, time float64, env Environment, carrier *Carrier, place Rigid) Vec3 {
	h := math.Max(height, 2)

	// Mean wind: power-law shear from the 10 m reference, Ekman veer, and a
	// winds-aloft strengthening saturating at 10 km. The match's wind is level
	// wherever it blows: east and south, the same over the whole earth.
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
		phase := 2 * math.Pi * (home.X*dx + home.Z*dz) / wavelength
		factor := 0.15 * strength * math.Sin(phase+2*math.Pi*noise(env.Seed, 920+k))
		mean.X += dx * factor
		mean.Z += dz * factor
	}

	// Cloud convection: extra gust intensity inside convective cells, and
	// the thermal/sink structure beneath their bases (convection.go).
	chop, draft := convection(home.X, home.Z, height, env.Cloud)
	mean.Y += draft

	// Turbulence: a frozen field of sinusoids per axis, wavelengths log
	// spaced, amplitudes Dryden-ish (energy toward the longer wavelengths),
	// intensity and scale reduced near the surface (MIL-8785C low-altitude
	// character), advected past the aircraft with the mean wind and time.
	if env.Turbulence > 0 || chop > 0 {
		scale := clamp(h, 50, 533)  // Dryden scale length
		low := clamp(h/300, 0.4, 1) // near-surface intensity knockdown
		sigma := env.Turbulence*low + chop
		// Energy per octave: Kolmogorov 2/3 rolloff below the scale length, flat
		// above it - long wavelengths carry the felt bumps at 0.2-1.5 Hz. Inverting
		// these weights puts most energy into a 3 Hz buzz.
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
				phase := 2*math.Pi*((home.X+speed*time)*dx+(home.Z+speed*time)*dz)/wavelength + 2*math.Pi*noise(env.Seed, 100+i)
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
		deck := carrier.local(home, time, Identity) // the point in the deck's own frame: x to the bow, y up, z to starboard
		behind := -deck.X                           // + when astern
		lateral := math.Abs(deck.Z)
		if behind > 0 && behind < burble_reach && lateral < 60 && deck.Y > -5 && deck.Y < 60 {
			_, attitude := carrier.Pose(time)
			back := place.Inverse()
			forward := back.Vector(attitude.Rotate(Vec3{X: 1}))
			over := forward.Scale(-carrier.Way(time)).Subtract(mean).Length() // wind over deck
			fade := (1 - behind/burble_reach) * (1 - lateral/60) * math.Exp(-deck.Y/25)
			mean = mean.Add(forward.Scale(0.3 * over * fade))                                       // deficit along the deck wind
			mean = mean.Subtract(back.Vector(attitude.Rotate(Vec3{Y: 1})).Scale(0.4 * over * fade)) // the sink in the groove
		}
	}
	return mean
}
