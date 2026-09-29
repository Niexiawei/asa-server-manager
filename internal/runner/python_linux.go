//go:build linux

package runner

import (
	"asa-server/pkg/pyfinder"
)

// umuInterpreter is the single choke point for "which Python runs umu-run":
// the same resolver (inside the host's *umu.Runtime, configured with
// runner.Config's PythonBin override) that every launch uses, so preflight
// and the launch can never disagree. The discovery/version-check mechanism
// lives in pkg/pyfinder; see docs/UMU_PYTHON_DISCOVERY_PLAN.md.
//
// A failure is fatal to a launch: callers surface it rather than letting the
// zipapp's shebang fall back to a possibly-too-old system python3.
func umuInterpreter() (pyfinder.Info, error) {
	return hostFor(getConfig()).Umu().Interpreter()
}

// pythonProblem turns a resolve failure into a preflight Problem (nil on success).
func pythonProblem() *Problem {
	_, err := umuInterpreter()
	if err == nil {
		return nil
	}

	name := "python3"
	if pe, ok := pyfinder.AsError(err); ok {
		name = pe.Name
	}

	return &Problem{Name: name, Detail: err.Error(), Fix: pyfinder.FixHint}
}

func runtimePython() RuntimePythonInfo {
	info, err := umuInterpreter()
	if err != nil {
		return RuntimePythonInfo{Resolved: false}
	}

	return RuntimePythonInfo{
		Resolved: true,
		Path:     info.Path,
		Version:  info.Version(),
		Source:   info.Source,
	}
}
