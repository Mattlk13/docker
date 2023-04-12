package trap // import "github.com/docker/docker/cmd/dockerd/trap"

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const (
	// Immediately terminate the process when this many SIGINT or SIGTERM
	// signals are received.
	forceQuitCount = 3
)

// Trap sets up a simplified signal "trap", appropriate for common
// behavior expected from a vanilla unix command-line tool in general
// (and the Docker engine in particular).
//
// The first time a SIGINT or SIGTERM signal is received, `cleanup` is called in
// a new goroutine.
//
// If SIGINT or SIGTERM are received 3 times, the process is terminated
// immediately with an exit code of 128 + the signal number.
func Trap(cleanup func(), logger interface {
	Info(args ...interface{})
}) {
	c := make(chan os.Signal, forceQuitCount)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		var interruptCount int
		for sig := range c {
			logger.Info(fmt.Sprintf("Processing signal '%v'", sig))
			if interruptCount < forceQuitCount {
				interruptCount++
				// Initiate the cleanup only once
				if interruptCount == 1 {
					go cleanup()
				}
				continue
			}

			logger.Info("Forcing docker daemon shutdown without cleanup; 3 interrupts received")
			os.Exit(128 + int(sig.(syscall.Signal)))
		}
	}()
}
