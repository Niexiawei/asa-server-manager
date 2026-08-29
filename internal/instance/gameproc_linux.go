//go:build linux

package instance

import (
	"asa-server/pkg/procx"
	"strings"
)

// queryGameProcesses finds the running ArkAscendedServer.exe by command line
// only — image-name matching cannot work here.
//
// Under Proton the game's /proc/<pid>/exe points at
// ".../GE-Proton10-34/files/bin/wine64-preloader" and /proc/<pid>/comm is
// "GameThread", so procx.QueryProcess's name filter ("ArkAscendedServer.exe")
// never matches and the query returns an empty set every single time. That
// silently broke waitForGamePID into a guaranteed 30s timeout, which reported
// a failed start while the game process was in fact up — leaving an orphaned
// process tree behind on every attempt. See
// docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §2.5.
//
// Dropping the name filter alone isn't enough: umu-run, pv-adverb, proton and
// umu.exe all carry the exe's path on their own command lines too. What
// separates the game from its four wrappers is the path *form* — only the
// Wine side sees "Z:\...\ArkAscendedServer.exe", every wrapper carries the
// Unix "/.../ArkAscendedServer.exe" — so the backslash is the discriminator.
func queryGameProcesses(cmdlineMarker string) ([]procx.Win32Process, error) {
	procs, err := procx.QueryProcess("", cmdlineMarker)
	if err != nil {
		return nil, err
	}
	out := procs[:0]
	for _, p := range procs {
		if strings.Contains(p.CommandLine, `\`+arkExeName) {
			out = append(out, p)
		}
	}
	return out, nil
}
