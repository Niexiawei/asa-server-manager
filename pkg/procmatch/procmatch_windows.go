//go:build windows

package procmatch

import "asa-server/pkg/procx"

// Find finds the running game process by image name — the exe runs directly
// on Windows, so Win32_Process.Name reliably matches exeNames[0] and the WMI
// query narrows the scan before the command-line filter runs. No loader
// ambiguity to resolve here (contrast procmatch_linux.go).
func (m *Matcher) Find(cmdlineMarker string) (procx.Win32Process, bool, error) {
	procs, err := procx.QueryProcess(m.exeNames[0], cmdlineMarker)
	if err != nil {
		return procx.Win32Process{}, false, err
	}
	if len(procs) == 0 {
		return procx.Win32Process{}, false, nil
	}
	return procs[0], true, nil
}
