// Mochi world: Atmosphere
// Copyright © 2026 Mochi OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// Two-layer International Standard Atmosphere (troposphere + lower
// stratosphere), parameterised by the match's sea-level temperature offset
// and pressure (Environment). Self-contained behind air(h) so a piecewise
// temperature profile (e.g. the trade-wind inversion) can slot in later
// without touching callers.

package flight

import (
	"math"
)

// Air is the local atmospheric state at an altitude.
type Air struct {
	Density     float64 // kg/m³
	Pressure    float64 // Pa
	Temperature float64 // K
	Sound       float64 // m/s
}

const (
	isa_temperature = 288.15   // K at sea level
	isa_pressure    = 101325.0 // Pa at sea level
	isa_lapse       = 0.0065   // K/m to the tropopause
	isa_tropopause  = 11000.0  // m
	gas             = 287.053  // J/(kg·K), dry air
	gravity         = 9.80665  // m/s²
	heat            = 1.4      // ratio of specific heats
)

// air evaluates the atmosphere at altitude h (m) for the environment's
// sea-level conditions.
func air(h float64, env Environment) Air {
	surface := isa_temperature + env.Temperature
	base := isa_pressure
	if env.Pressure > 0 {
		base = env.Pressure
	}
	if h < 0 {
		h = 0
	}
	var temperature, pressure float64
	if h <= isa_tropopause {
		temperature = surface - isa_lapse*h
		pressure = base * math.Pow(temperature/surface, gravity/(isa_lapse*gas))
	} else {
		top := surface - isa_lapse*isa_tropopause
		at := base * math.Pow(top/surface, gravity/(isa_lapse*gas))
		temperature = top
		pressure = at * math.Exp(-gravity*(h-isa_tropopause)/(gas*top))
	}
	return Air{
		Density:     pressure / (gas * temperature),
		Pressure:    pressure,
		Temperature: temperature,
		Sound:       math.Sqrt(heat * gas * temperature),
	}
}
