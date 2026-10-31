// Mochi world: Land
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The earth's land beyond the charted fields: flat soft ground at sea level
// wherever Natural Earth's 1:50m outline says land, the same outline the
// client draws its low quality global map from (apps/air/tools/world.py, #162).
// Both hosts read one file, land.bin: rows a fixed arc apart from the north
// pole, each the longitudes where it passes between sea and land.

package flight

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// Land is the land outline as the hosts read it. A place is land when an odd
// number of its row's toggles lie at or west of it.
type Land struct {
	row     float64  // metres of equatorial arc between rows, from 90 N
	unit    float64  // metres of equatorial arc in a toggle's unit, from 180 W
	units   uint32   // units around the earth
	starts  []uint32 // each row's first toggle in toggles, and one past the last row's
	toggles []uint32
}

// Outline reads land.bin: b"LAND", then little-endian u8 version 1, u16 metres
// between rows, u16 metres in a unit, u32 rows, u32 units around the earth;
// each row an unsigned LEB128 count and that many LEB128 gaps, the first from 0.
func Outline(data []byte) (*Land, error) {
	if len(data) < 17 || string(data[:4]) != "LAND" || data[4] != 1 {
		return nil, errors.New("land: not a version 1 land file")
	}
	land := &Land{
		row:   float64(binary.LittleEndian.Uint16(data[5:])),
		unit:  float64(binary.LittleEndian.Uint16(data[7:])),
		units: binary.LittleEndian.Uint32(data[13:]),
	}
	rows := int(binary.LittleEndian.Uint32(data[9:]))
	if land.row <= 0 || land.unit <= 0 || rows <= 0 {
		return nil, errors.New("land: empty grid")
	}
	at := 17
	next := func() (uint32, error) {
		value, size := binary.Uvarint(data[at:])
		if size <= 0 || value > math.MaxUint32 {
			return 0, errors.New("land: truncated")
		}
		at += size
		return uint32(value), nil
	}
	land.starts = make([]uint32, 0, rows+1)
	for r := 0; r < rows; r++ {
		land.starts = append(land.starts, uint32(len(land.toggles)))
		count, err := next()
		if err != nil {
			return nil, err
		}
		last := uint32(0)
		for k := uint32(0); k < count; k++ {
			gap, err := next()
			if err != nil {
				return nil, err
			}
			last += gap
			land.toggles = append(land.toggles, last)
		}
	}
	land.starts = append(land.starts, uint32(len(land.toggles)))
	if at != len(data) {
		return nil, errors.New("land: trailing bytes")
	}
	return land, nil
}

// At reports whether a place, radians, is land.
func (l *Land) At(latitude, longitude float64) bool {
	rows := len(l.starts) - 1
	r := int((math.Pi/2 - latitude) * Equator / l.row)
	if r < 0 {
		r = 0
	} else if r >= rows {
		r = rows - 1
	}
	east := math.Mod(longitude+math.Pi, 2*math.Pi)
	if east < 0 {
		east += 2 * math.Pi
	}
	unit := uint32(east * Equator / l.unit)
	if unit >= l.units {
		unit = l.units - 1 // the last unit runs past the antimeridian
	}
	row := l.toggles[l.starts[r]:l.starts[r+1]]
	west := sort.Search(len(row), func(k int) bool { return row[k] > unit }) // toggles at or west of the place
	return west%2 == 1
}
