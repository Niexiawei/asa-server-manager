package installer

import (
	cfgpkg "asa-server/internal/config"
	"os"
	"path/filepath"
)

// InstallStatus is a snapshot of which base-environment pieces this package
// installs are present on disk under the current BaseDir. It deliberately
// does NOT cover the Linux Wine/Proton runtime — that is runner.CheckRuntime's
// job (see docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.1).
type InstallStatus struct {
	SteamCmdReady     bool // {SteamCmdDir}/steamcmd.exe (win) or steamcmd.sh (linux)
	ServerBinaryReady bool // {ServerFilesDir}/.../Win64/ArkAscendedServer.exe
	ServerConfigReady bool // {ServerFilesDir}/.../Saved/Config/WindowsServer (first-run config generated)
}

// Ready reports whether everything a game instance needs from this package is
// in place.
func (s InstallStatus) Ready() bool {
	return s.SteamCmdReady && s.ServerBinaryReady && s.ServerConfigReady
}

// CheckInstalled reports the on-disk state of SteamCMD and the ARK server
// installation. Pure stat calls, no side effects. Paths mirror
// VerifyServerInstallation / DownloadAndUpdateArkServer so the judgement can't
// drift from what those functions actually produce.
func CheckInstalled() InstallStatus {
	var s InstallStatus

	if fi, err := os.Stat(filepath.Join(cfgpkg.SteamCmdDir, steamCmdBinaryName)); err == nil && !fi.IsDir() {
		s.SteamCmdReady = true
	}
	if fi, err := os.Stat(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")); err == nil && !fi.IsDir() {
		s.ServerBinaryReady = true
	}
	if fi, err := os.Stat(filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")); err == nil && fi.IsDir() {
		s.ServerConfigReady = true
	}
	return s
}
