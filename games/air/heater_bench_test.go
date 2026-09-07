package air

import (
	"testing"

	"world/games/air/round"
)

func BenchmarkHeat(b *testing.B) {
	c := recorded()[1]
	shooter := round.Target{Position: c.shooterP, Velocity: c.shooterV}
	target := round.Target{Position: c.targetP, Velocity: c.targetV}
	for i := 0; i < b.N; i++ {
		Heat(shooter, target, c.swing, 1, 0)
	}
}
