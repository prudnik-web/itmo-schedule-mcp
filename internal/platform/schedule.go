package platform

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// InstallSchedule keeps its own executable copy, so moving a download or source
// checkout cannot break unattended runs. All registrations belong to this user.
func InstallSchedule(ctx context.Context, executable, dir string) error {
	dir, err := absolutePath(dir)
	if err != nil {
		return err
	}
	if err = EnsurePrivateDir(dir); err != nil {
		return err
	}
	binary, err := installExecutable(executable, dir)
	if err != nil {
		return err
	}
	name := profileName(dir)
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		folder := filepath.Join(home, "Library", "LaunchAgents")
		if err = os.MkdirAll(folder, 0755); err != nil {
			return err
		}
		path := filepath.Join(folder, name+".plist")
		uid, err := currentUserID()
		if err != nil {
			return err
		}
		domain := "gui/" + uid
		if exec.CommandContext(ctx, "/bin/launchctl", "print", domain+"/"+name).Run() == nil {
			if err = runCommand(ctx, "/bin/launchctl", "bootout", domain+"/"+name); err != nil {
				return err
			}
		}
		if err = atomicWrite(path, []byte(launchdConfig(name, binary, dir)), 0600); err != nil {
			return err
		}
		if err = runCommand(ctx, "/bin/launchctl", "bootstrap", domain, path); err != nil {
			return fmt.Errorf("launchd setup requires a logged-in macOS desktop session: %w", err)
		}
		return saveSchedule(dir, "launchd", name, binary, []string{path})
	case "linux":
		folder, err := systemdDir()
		if err != nil {
			return err
		}
		if err = os.MkdirAll(folder, 0755); err != nil {
			return err
		}
		service, timer := filepath.Join(folder, name+".service"), filepath.Join(folder, name+".timer")
		if err = atomicWrite(service, []byte(systemdService(binary, dir)), 0600); err != nil {
			return err
		}
		if err = atomicWrite(timer, []byte(systemdTimer(name)), 0600); err != nil {
			return err
		}
		if err = runCommand(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("a running systemd user manager is required: %w", err)
		}
		if err = runCommand(ctx, "systemctl", "--user", "enable", "--now", name+".timer"); err != nil {
			return err
		}
		return saveSchedule(dir, "systemd-user", name, binary, []string{service, timer})
	case "windows":
		sid, err := currentUserID()
		if err != nil {
			return err
		}
		launcher, err := windowsPowerShell()
		if err != nil {
			return err
		}
		start := time.Now().Truncate(time.Hour).Add(7 * time.Minute)
		if !start.After(time.Now()) {
			start = start.Add(time.Hour)
		}
		path := filepath.Join(dir, "scheduled-task.xml")
		if err = atomicWrite(path, []byte(windowsTaskConfig(launcher, binary, dir, sid, start)), 0600); err != nil {
			return err
		}
		if err = runCommand(ctx, "schtasks.exe", "/Create", "/TN", name, "/XML", path, "/F"); err != nil {
			return err
		}
		return saveSchedule(dir, "windows-task-scheduler", name, binary, []string{path})
	default:
		return fmt.Errorf("automatic scheduling is unavailable on %s", runtime.GOOS)
	}
}

// RemoveSchedule unregisters this profile's job and keeps account state intact.
func RemoveSchedule(ctx context.Context, dir string) error {
	dir, err := absolutePath(dir)
	if err != nil {
		return err
	}
	name := profileName(dir)
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		uid, err := currentUserID()
		if err != nil {
			return err
		}
		target := "gui/" + uid + "/" + name
		if exec.CommandContext(ctx, "/bin/launchctl", "print", target).Run() == nil {
			if err = runCommand(ctx, "/bin/launchctl", "bootout", target); err != nil {
				return err
			}
		}
		if err = removeFile(filepath.Join(home, "Library", "LaunchAgents", name+".plist")); err != nil {
			return err
		}
	case "linux":
		folder, err := systemdDir()
		if err != nil {
			return err
		}
		if err = runCommand(ctx, "systemctl", "--user", "disable", "--now", name+".timer"); err != nil {
			return err
		}
		if err = runCommand(ctx, "systemctl", "--user", "stop", name+".service"); err != nil {
			return err
		}
		for _, suffix := range []string{".timer", ".service"} {
			if err = removeFile(filepath.Join(folder, name+suffix)); err != nil {
				return err
			}
		}
		if err = runCommand(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
	case "windows":
		if err = runCommand(ctx, "schtasks.exe", "/Delete", "/TN", name, "/F"); err != nil {
			return err
		}
		if err = removeFile(filepath.Join(dir, "scheduled-task.xml")); err != nil {
			return err
		}
	default:
		return fmt.Errorf("automatic scheduling is unavailable on %s", runtime.GOOS)
	}
	return removeFile(filepath.Join(dir, "schedule.json"))
}

func systemdDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user"), nil
}

func launchdConfig(name, binary, dir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>sync</string><string>--dir</string><string>%s</string><string>--scheduled</string></array>
<key>StartCalendarInterval</key><dict><key>Minute</key><integer>7</integer></dict>
<key>RunAtLoad</key><true/>
<key>Umask</key><integer>63</integer>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, escapedXML(name), escapedXML(binary), escapedXML(dir), escapedXML(filepath.Join(dir, "sync.log")), escapedXML(filepath.Join(dir, "sync-error.log")))
}

// systemd performs its own quoting and %-specifier expansion. ExecStart uses
// the ':' executable prefix to disable environment expansion entirely, so '$'
// remains literal even in the executable's path. No shell interprets the text.
func systemdArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s) + `"`
}

func systemdService(binary, dir string) string {
	return "[Unit]\nDescription=Sync personal ITMO classes to Google Calendar\n\n[Service]\nType=oneshot\nUMask=0077\nExecStart=" + systemdArg(":"+binary) + " sync --dir " + systemdArg(dir) + " --scheduled\nTimeoutStartSec=11min\n"
}

func systemdTimer(name string) string {
	return "[Unit]\nDescription=Hourly ITMO calendar sync\n\n[Timer]\nOnCalendar=*-*-* *:07:00\nPersistent=true\nUnit=" + name + ".service\n\n[Install]\nWantedBy=timers.target\n"
}

// Task Scheduler expands %ENV% in action paths/arguments. Keep user paths out
// of those fields altogether: a fixed built-in PowerShell launcher decodes
// base64 path data and invokes the executable without evaluating the path text.
func windowsLaunchArguments(binaryPath, dir string) string {
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	script := fmt.Sprintf("$ErrorActionPreference='Stop';$p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'));$d=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'));& $p 'sync' '--dir' $d '--scheduled';exit $LASTEXITCODE", encode(binaryPath), encode(dir))
	chars := utf16.Encode([]rune(script))
	data := make([]byte, len(chars)*2)
	for i, c := range chars {
		binary.LittleEndian.PutUint16(data[i*2:], c)
	}
	return "-NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(data)
}

func windowsTaskConfig(launcher, binary, dir, sid string, start time.Time) string {
	arguments := windowsLaunchArguments(binary, dir)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo><Description>Hourly ITMO calendar sync for the current user</Description></RegistrationInfo>
<Triggers><TimeTrigger><Repetition><Interval>PT1H</Interval><StopAtDurationEnd>false</StopAtDurationEnd></Repetition><StartBoundary>%s</StartBoundary><Enabled>true</Enabled></TimeTrigger><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
<Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled><ExecutionTimeLimit>PT11M</ExecutionTimeLimit></Settings>
<Actions Context="Author"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions>
</Task>
`, start.Format("2006-01-02T15:04:05"), escapedXML(sid), escapedXML(sid), escapedXML(launcher), escapedXML(arguments))
}
