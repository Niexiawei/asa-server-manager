//go:build windows

package processjob

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessJob represents a Windows Job Object bound process.
type ProcessJob struct {
	job  windows.Handle
	cmd  *exec.Cmd
	once sync.Once
}

// createJob creates a Job Object with KILL_ON_JOB_CLOSE enabled.
func createJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(job)
		return 0, err
	}

	return job, nil
}

// Start starts cmd and binds it to a Windows Job Object.
// Closing the returned ProcessJob will terminate the entire process tree.
func Start(ctx context.Context, cmd *exec.Cmd) (*ProcessJob, error) {
	if cmd == nil {
		return nil, errors.New("processjob: cmd is nil")
	}

	job, err := createJob()
	if err != nil {
		return nil, err
	}

	// Ensure a new process group to avoid console signal interference
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP

	// Start process
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}

	// Convert PID -> HANDLE (critical step)
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_ALL_ACCESS,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = cmd.Process.Kill()
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(procHandle)

	// Bind process to Job Object
	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		_ = cmd.Process.Kill()
		windows.CloseHandle(job)
		return nil, err
	}

	pj := &ProcessJob{
		job: job,
		cmd: cmd,
	}

	// Optional: auto-close on ctx cancel
	if ctx != nil {
		go func() {
			<-ctx.Done()
			pj.Close()
		}()
	}

	return pj, nil
}

// Close terminates all processes in the Job Object.
// Safe to call multiple times.
func (p *ProcessJob) Close() {
	if p == nil {
		return
	}

	p.once.Do(func() {
		if p.job != 0 {
			windows.CloseHandle(p.job)
			p.job = 0
		}
	})
}

// Wait waits for the root process to exit.
func (p *ProcessJob) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}
