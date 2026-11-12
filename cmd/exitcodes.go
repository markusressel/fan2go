package cmd

import (
	"github.com/markusressel/fan2go/internal/configuration"
)

// Exit codes of fan2go. They are documented in the README ("Exit codes") and in docs/daemon.md.
const (
	// ExitCodeOk is used when fan2go stopped normally, e.g. after SIGINT or SIGTERM.
	ExitCodeOk = 0
	// ExitCodeError is used for all errors that are not config errors, e.g. when no configured fan could be found.
	// Restarting fan2go may help with these (e.g. a driver that is loaded late).
	ExitCodeError = 1
	// ExitCodeConfigError (EX_CONFIG from sysexits.h) is used when the config file can't be found, read, decoded or
	// validated, or when the objects it describes can't be created (see configuration.ConfigError).
	// fan2go.service doesn't restart fan2go after this code (RestartPreventExitStatus=78), since a restart
	// can't fix the config.
	ExitCodeConfigError = 78
)

// exitCode returns the exit code for the given error returned by the root command
func exitCode(err error) int {
	switch {
	case err == nil:
		return ExitCodeOk
	case configuration.IsConfigError(err):
		return ExitCodeConfigError
	default:
		return ExitCodeError
	}
}
