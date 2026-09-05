// Package procmatch picks which running OS process is "the real game
// process" for a Wine/Proton-hosted game server that may run behind a loader
// exe. It does not know about ASA, ArkApi, or instances — the caller injects
// the exe names and thread-name marker; the package only knows the matching
// rules.
//
// The rules exist because a loader that re-execs the target exe with its own
// command line unmodified produces two processes with byte-identical command
// lines (loader and game); see New for the tie-breaker.
//
// Deliberately free of a //go:build tag: the matching rules themselves
// (isWineSideCmdline, pick) are plain string comparisons with no
// platform-specific API, so they can be unit-tested on any host — only Find
// (procmatch_windows.go / procmatch_linux.go) needs a real OS process query.
package procmatch

import (
	"asa-server/pkg/procx"
	"strings"
)

// Matcher decides which running process is "the real game process".
type Matcher struct {
	// exeNames lists every exe name that can appear on the Wine-side
	// (Windows-path-form) command line. exeNames[0] is the canonical name:
	// it is used both as the Windows image-name filter and as the
	// second-tier fallback marker on Linux when no candidate's comm matches.
	exeNames []string
	// commName is the process-thread name (Linux /proc/<pid>/comm) the real
	// game process carries, used to disambiguate a loader/game pair whose
	// command lines are identical.
	commName string
}

// New returns a Matcher. exeNames must be non-empty; exeNames[0] is the
// canonical/fallback exe name (see Matcher.exeNames).
func New(exeNames []string, commName string) *Matcher {
	return &Matcher{exeNames: append([]string(nil), exeNames...), commName: commName}
}

// candidate is a process whose command line looks like the game, plus its
// comm.
type candidate struct {
	Proc procx.Win32Process
	Comm string
}

// isWineSideCmdline reports whether cmdline is a Wine-side game/loader
// process: it must carry a Windows path form (backslash) immediately before
// one of the known exe names.
//
// Just checking for a backslash is not enough: a wrapper's own command line
// can contain one too (e.g. a Windows-style launcher path invoking a Unix
// target), so the backslash must be directly attached to the exe name.
func (m *Matcher) isWineSideCmdline(cmdline string) bool {
	for _, exe := range m.exeNames {
		if strings.Contains(cmdline, `\`+exe) {
			return true
		}
	}
	return false
}

// pick chooses the real game process among candidates.
//
// Two tiers, order matters:
//
//  1. comm == commName — the reliable signal when a loader and the game
//     share an identical command line.
//  2. No candidate has that comm: fall back to "the one whose command line
//     names exeNames[0]", the rule that holds when there is no loader in the
//     picture at all.
//
// Neither tier matching returns no match — better to report "not started
// yet" than to hand back a loader process: a loader is not the game's parent,
// so terminating its tree does not reach the game.
func (m *Matcher) pick(candidates []candidate) (procx.Win32Process, bool) {
	for _, c := range candidates {
		if c.Comm == m.commName {
			return c.Proc, true
		}
	}
	for _, c := range candidates {
		if strings.Contains(c.Proc.CommandLine, `\`+m.exeNames[0]) {
			return c.Proc, true
		}
	}
	return procx.Win32Process{}, false
}
