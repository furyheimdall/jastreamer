//go:build windows

package transcode

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var processJobs sync.Map

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func processStarted(command *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return err
	}
	processJobs.Store(command, job)
	return nil
}

func processFinished(command *exec.Cmd) {
	if value, ok := processJobs.LoadAndDelete(command); ok {
		windows.CloseHandle(value.(windows.Handle))
	}
}

func terminateProcessTree(command *exec.Cmd) {
	if value, ok := processJobs.LoadAndDelete(command); ok {
		windows.TerminateJobObject(value.(windows.Handle), 1)
		windows.CloseHandle(value.(windows.Handle))
		return
	}
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}

func isTerminated(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}
