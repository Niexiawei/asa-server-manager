//go:build windows

package procmatch

import "asa-server/pkg/procx"

// Find finds the running game process by image name — the exe runs directly
// on Windows, so Win32_Process.Name reliably matches exeNames[0] and the WMI
// query narrows the scan before the command-line filter runs. No loader
// ambiguity to resolve here (contrast procmatch_linux.go).
//
// prefilter goes into the WQL LIKE clause; accept, when non-nil, is the exact
// judgement applied to what comes back (LIKE is a substring match, and its
// '_' is a wildcard on top of that).
func (m *Matcher) Find(prefilter string, accept func(cmdline string) bool) (procx.Win32Process, bool, error) {
	procs, err := procx.QueryProcess(m.exeNames[0], prefilter)
	if err != nil {
		return procx.Win32Process{}, false, err
	}
	for _, p := range procs {
		if accepts(accept, p.CommandLine) {
			return p, true, nil
		}
	}
	return procx.Win32Process{}, false, nil
}
