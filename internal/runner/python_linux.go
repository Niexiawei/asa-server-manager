//go:build linux

package runner

import (
	"asa-server/pkg/pyfinder"
)

// pyResolver is this process's single Python-interpreter resolver for
// umu-run. The discovery/version-check mechanism lives in pkg/pyfinder;
// this file only wires it to runner.Config's PythonBin override. See
// docs/UMU_PYTHON_DISCOVERY_PLAN.md.
var pyResolver = pyfinder.New()

// umuInterpreter is the single choke point for "which Python runs umu-run".
// A failure is fatal to a launch: callers surface it rather than letting the
// zipapp's shebang fall back to a possibly-too-old system python3.
func umuInterpreter() (pyfinder.Info, error) {
	return pyResolver.Resolve(getConfig().PythonBin)
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
