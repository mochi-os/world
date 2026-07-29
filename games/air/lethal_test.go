package air

import (
	"fmt"
	"math"
	"testing"

	"world/game"
	"world/games/air/flight"
)

// TestDroneKill (#219, the measurement harness #215 will retune against): can a
// bot finish a COMPLIANT target? Every other metric measures the bots' DEFENCE,
// and the scripted harness cannot answer this because its attacker never stops
// attacking — the bot is never handed an easy kill to see whether it takes one.
// A drone weaves gently and never fights back.
//
// It LOGS rather than asserts, deliberately. The property worth gating is that
// lethality ladders (ace >= veteran), and that is currently FALSE: measured
// 2026-07-29, veteran kills 5/6 while the ace manages 2/6 and takes half again
// as long doing it. That inversion is the open defect tracked in #215, so
// asserting it here would just paint the tree red for a fault we already know
// about. Turn it into a gate as part of that retune, not before.
//
// The ablation runs each ace-only parameter back to its veteran value, one at a
// time, then in combination. No SINGLE parameter recovers it; veteran wander +
// open + library together restore 4/6 at 105 s. Note especially that positional
// advantage does NOT predict kills — library 3 gives the ace its best advantage
// of all (19.8%) and nearly its worst kill rate.
func TestDroneKill(t *testing.T) {
	if testing.Short() {
		t.Skip("several simulated minutes per tier")
	}
	const seconds = 180
	// Ablation: give the ace each of the veteran's values in turn, one at a
	// time, to find which of the ace-only parameters costs it the kill. The
	// roster parser only accepts the five known level names, so each variant is
	// installed over "ace" for its run and restored after.
	vet, base := skills["veteran"], skills["ace"]
	defer func() { skills["ace"] = base }()
	type variant struct {
		name string
		make func(skill) skill
	}
	runs := []variant{
		{"veteran", func(s skill) skill { return vet }},
		{"ace", func(s skill) skill { return s }},
		{"ace+vetOpen", func(s skill) skill { s.open = vet.open; return s }},
		{"ace+vetFloor", func(s skill) skill { s.floor = vet.floor; return s }},
		{"ace+vetCommit", func(s skill) skill { s.commit = vet.commit; return s }},
		{"ace+vetWander", func(s skill) skill { s.wander = vet.wander; return s }},
		{"ace+vetDiscipline", func(s skill) skill { s.discipline = vet.discipline; return s }},
		{"ace+vetLibrary", func(s skill) skill { s.library = vet.library; return s }},
		{"ace+vetShot", func(s skill) skill { s.wander, s.open = vet.wander, vet.open; return s }},
		{"ace+vetShot+Lib", func(s skill) skill { s.wander, s.open, s.library = vet.wander, vet.open, vet.library; return s }},
	}
	for _, run := range runs {
		level := "ace"
		skills["ace"] = run.make(base)
		kills, tries := 0, 0
		var times []float64
		advantage, firing, shots := 0, 0, 0
		total := 0
		modes := map[string]int{}
		for seed := 1; seed <= 6; seed++ {
			g := New()
			made, _ := g.Create(game.Session{Identifier: fmt.Sprintf("d%s%d", level, seed), Game: "air",
				Mode: "furball", Capacity: 8, Seed: uint64(seed),
				Parameters: map[string]any{"bots": map[string]any{level: 1.0, "drone": 1.0}}})
			i := made.(*instance)
			var hunter, prey *craft
			for _, s := range i.slots() {
				if i.aircraft[s].brain != nil {
					hunter = i.aircraft[s]
				} else {
					prey = i.aircraft[s]
				}
			}
			if hunter == nil || prey == nil {
				t.Fatalf("%s: roster wrong", level)
			}
			tries++
			killed := -1.0
			for tick := uint64(0); tick < 60*seconds; tick++ {
				if hunter.model == nil || prey.model == nil || hunter.brain == nil {
					break // a respawn replaced a craft: stop this seed rather than read a stale one
				}
				aliveBefore := prey.alive
				i.Step(tick, nil)
				if hunter.model == nil || prey.model == nil || hunter.brain == nil {
					break
				}
				total++
				modes[hunter.brain.mode]++
				if hunter.latest.Fire {
					firing++
					shots++
				}
				// Advantage: inside gun range and roughly behind him.
				h, p := &hunter.model.State, &prey.model.State
				to := p.Position.Subtract(h.Position)
				rng := to.Length()
				if rng > 1 {
					nose := h.Attitude.Rotate(flight.Vec3{X: 1})
					off := math.Acos(clamp(nose.Dot(to.Scale(1/rng)), -1, 1)) * 180 / math.Pi
					if rng < 900 && off < 30 {
						advantage++
					}
				}
				if aliveBefore && !prey.alive && killed < 0 {
					killed = float64(tick) / 60
					kills++
					times = append(times, killed)
					break
				}
			}
		}
		mean := 0.0
		for _, v := range times {
			mean += v
		}
		if len(times) > 0 {
			mean /= float64(len(times))
		}
		top, best := "", 0
		for m, c := range modes {
			if c > best {
				top, best = m, c
			}
		}
		fmt.Printf("%-18s kills %d/%d   mean time-to-kill %5.1fs   advantage %4.1f%%   trigger %4.1f%%   commonest mode %s\n",
			run.name, kills, tries, mean, 100*float64(advantage)/float64(total), 100*float64(firing)/float64(total), top)
	}
}
