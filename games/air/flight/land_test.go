// Mochi world: Land tests
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

package flight

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// earth reads the land file the world server embeds (games/air/maps/land.bin),
// which apps/air/tools/world.py writes there and into the client's public maps.
func earth(t *testing.T) *Land {
	t.Helper()
	data, err := os.ReadFile("../maps/land.bin")
	if err != nil {
		t.Fatal(err)
	}
	land, err := Outline(data)
	if err != nil {
		t.Fatal(err)
	}
	return land
}

func degrees(latitude, longitude float64) (float64, float64) {
	return latitude * math.Pi / 180, longitude * math.Pi / 180
}

func TestLandKnowsTheEarthsLandFromItsSea(t *testing.T) {
	land := earth(t)
	for _, c := range []struct {
		name                string
		latitude, longitude float64
		land                bool
	}{
		{"Paris", 48.86, 2.35, true},
		{"London", 51.5, -0.12, true},
		{"the mid-Atlantic", 30, -40, false},
		{"the North Sea", 56, 3, false},
		{"Midway, which Natural Earth's 1:50m leaves to the map", 28.21, -177.37, false},
		{"Honolulu", 21.31, -157.86, true},
		{"the South Pole", -89.5, 0, true},
		{"Viti Levu, Fiji, west of the antimeridian", -17.8, 178.0, true},
		{"Chukotka east of the antimeridian", 66, -175, true},
		{"Chukotka west of the antimeridian", 66, 179, true},
		{"the Caspian, a hole in Eurasia", 42, 51, false},
	} {
		if got := land.At(degrees(c.latitude, c.longitude)); got != c.land {
			t.Errorf("%s: land %v, want %v", c.name, got, c.land)
		}
	}
}

// grid builds a land file by the format's rules: rows of toggles, LEB128.
func grid(row, unit uint16, units uint32, rows [][]uint32) []byte {
	out := []byte("LAND")
	head := make([]byte, 13)
	head[0] = 1
	binary.LittleEndian.PutUint16(head[1:], row)
	binary.LittleEndian.PutUint16(head[3:], unit)
	binary.LittleEndian.PutUint32(head[5:], uint32(len(rows)))
	binary.LittleEndian.PutUint32(head[9:], units)
	out = append(out, head...)
	for _, toggles := range rows {
		out = binary.AppendUvarint(out, uint64(len(toggles)))
		last := uint32(0)
		for _, x := range toggles {
			out = binary.AppendUvarint(out, uint64(x-last))
			last = x
		}
	}
	return out
}

func TestLandReadsEachRowsTogglesByTheOddRule(t *testing.T) {
	units := uint32(math.Ceil(2 * math.Pi * Equator / 64))
	land, err := Outline(grid(512, 64, units, [][]uint32{{}, {100, 200, 300, units}}))
	if err != nil {
		t.Fatal(err)
	}
	latitude := math.Pi/2 - 1.5*512/Equator // the second row's middle
	east := func(unit float64) float64 { return -math.Pi + (unit+0.5)*64/Equator }
	for _, c := range []struct {
		unit float64
		land bool
	}{{99, false}, {100, true}, {199, true}, {200, false}, {300, true}} { // the last unit's middle lies past 180 E: the antimeridian case below covers it
		if got := land.At(latitude, east(c.unit)); got != c.land {
			t.Errorf("unit %v: land %v, want %v", c.unit, got, c.land)
		}
	}
	if land.At(math.Pi/2-0.5*512/Equator, east(150)) {
		t.Error("the first row has no toggles: all sea")
	}
	if !land.At(latitude, math.Pi-1e-12) {
		t.Error("the place just west of the antimeridian lies in the run to the earth's end")
	}
}

func TestLandRefusesABrokenFile(t *testing.T) {
	good := grid(512, 64, 1000, [][]uint32{{1, 2}})
	for name, data := range map[string][]byte{
		"empty":     nil,
		"magic":     append([]byte("LAMP"), good[4:]...),
		"version":   append(append([]byte{}, good[:4]...), append([]byte{2}, good[5:]...)...),
		"truncated": good[:len(good)-1],
		"trailing":  append(append([]byte{}, good...), 0),
	} {
		if _, err := Outline(data); err == nil {
			t.Errorf("%s: read without an error", name)
		}
	}
}

// A jet over land meets soft ground at sea level wherever it is: by its own
// place and altitude, in Home and in a frame moved far out under the jet, as
// the browser's core works.
func TestSurfaceFindsTheLandAtSeaLevelAnywhere(t *testing.T) {
	w := World{Sea: 3, Land: earth(t)}
	paris, atlantic := Place{}, Place{}
	paris.Latitude, paris.Longitude = degrees(48.86, 2.35)
	atlantic.Latitude, atlantic.Longitude = degrees(30, -40)
	for _, c := range []struct {
		name     string
		place    Place
		altitude float64
		found    bool
	}{
		{"low over Paris", paris, 40, true},
		{"above the reach over Paris", paris, Reach + 10, false},
		{"low over the Atlantic", atlantic, 40, false},
	} {
		p := c.place
		p.Altitude = c.altitude
		home := Home.Position(p) // in Home, as the server holds it: far below Home's plane out here
		height, kind, velocity, found := w.surface(home, 0, Identity)
		if found != c.found {
			t.Errorf("%s in Home: found %v, want %v", c.name, found, c.found)
			continue
		}
		if found && (kind != Soft || math.Abs(home.Y-height-c.altitude) > 1e-6 || velocity != (Vec3{})) {
			t.Errorf("%s in Home: kind %d, clearance %v, want soft ground %v below", c.name, kind, home.Y-height, c.altitude)
		}
		frame := Tangent(c.place.Latitude, c.place.Longitude) // the browser's frame, moved under the jet
		local := frame.Position(p)
		height, kind, _, found = w.surface(local, 0, Between(frame, Home))
		if found != c.found {
			t.Errorf("%s in its own frame: found %v, want %v", c.name, found, c.found)
			continue
		}
		if found && (kind != Soft || math.Abs(local.Y-height-c.altitude) > 1e-6) {
			t.Errorf("%s in its own frame: kind %d, clearance %v, want soft ground %v below", c.name, kind, local.Y-height, c.altitude)
		}
	}
	if _, _, _, found := (&World{Sea: 3}).surface(Home.Position(Place{Latitude: paris.Latitude, Longitude: paris.Longitude, Altitude: 40}), 0, Identity); found {
		t.Error("a world without land finds none")
	}
}
