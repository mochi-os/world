#!/usr/bin/env bash
# F/A-18C V-speed survey (#89): measures Vr, Vx, Vy, Vxse, Vyse, Vmc, Vs0,
# Vs1, Vapp, and the best sustained turn-rate speed by FLYING the flight
# model, at light and heavy weight, sea level / 15,000 ft / 30,000 ft.
# Several minutes of simulation; the harness lives in
# games/furball/flight/vspeeds_test.go (env-gated out of the normal suite).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
VSPEEDS=1 exec go test ./games/furball/flight -run '^TestVSpeeds$' -v -timeout 30m "$@"
