// Mochi world: Windows Service Control Manager integration.
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the Mochi
// Application Interface Exception - see license.txt and license-exception.md.
//
// Under the SCM, svc.Run calls Execute, which starts main_serve and translates
// Stop/Shutdown into a push on the stopping channel - the path an OS signal
// takes on Unix. Interactive mode falls through to main_serve directly.

//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
)

type service struct{}

// Execute is the SCM control loop. Returns when the service is fully stopped.
func (s *service) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (specific bool, code uint32) {
	const accept = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}

	done := make(chan int, 1)
	ready := func() {
		status <- svc.Status{State: svc.Running, Accepts: accept}
	}
	go func() {
		done <- main_serve(ready)
	}()

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				// Repeating the status twice is the standard pattern — some SCM
				// versions miss the first send right after Interrogate.
				status <- request.CurrentStatus
				time.Sleep(100 * time.Millisecond)
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				select {
				case stopping <- struct{}{}:
				default:
					// already requested
				}
				exit := <-done
				status <- svc.Status{State: svc.Stopped}
				return false, uint32(exit)
			}
		case exit := <-done:
			// main_serve exited on its own (e.g. startup failure).
			status <- svc.Status{State: svc.Stopped}
			return false, uint32(exit)
		}
	}
}

// windows_service_run hands off to the SCM if we were launched as a service.
// Returns true after svc.Run returns (whether successfully or with an error)
// so main() knows not to fall through to the interactive path.
func windows_service_run() bool {
	launched, err := svc.IsWindowsService()
	if err != nil || !launched {
		return false
	}
	if err := svc.Run("mochi-world", &service{}); err != nil {
		// SCM logs are inaccessible here; write to stderr so a manual
		// `mochi-world.exe` invocation surfaces the problem.
		log.Printf("Windows service handler exited with error: %v", err)
	}
	return true
}

// windows_service_redirect_logs sends stdout/stderr to a file so the SCM
// (which has no console) still produces a log. No-op when running
// interactively (a console is attached) — preserves the dev experience.
func windows_service_redirect_logs() {
	launched, err := svc.IsWindowsService()
	if err != nil || !launched {
		return
	}
	base := os.Getenv("ProgramData")
	if base == "" {
		return
	}
	directory := filepath.Join(base, "Mochi")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(directory, "mochi-world.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// Assigning os.Stdout is enough: log_writer, which stamps each line with
	// the time, writes through fmt.Printf. A log.SetOutput(file) here replaced
	// that writer, and with SetFlags(0) the service log then had no times at
	// all.
	os.Stdout = file
	os.Stderr = file
}
