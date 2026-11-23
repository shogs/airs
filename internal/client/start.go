package client

import (
	"crypto/sha256"
	"fmt"
	"html"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"airs/internal/paths"
)

// Label is the launchd job label for a state directory (unique per HOME, so
// tests never touch a real instance).
func Label(dirs paths.Dirs) string {
	h := sha256.Sum256([]byte(dirs.State))
	return fmt.Sprintf("dev.airs.server.%x", h[:4])
}

const maxLog = 1 << 20

// startDaemon starts `airs __serve` through a transient launchd job (not a
// login item: it lives only in this login session): on recent macOS only
// processes started by launchd (or a shell) can read the router's MAC address,
// which the network-trust check needs. If launchctl fails, the daemon is
// started directly; it then cannot identify networks and treats all as
// untrusted.
func startDaemon(dirs paths.Dirs) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if fi, err := os.Stat(dirs.Log()); err == nil && fi.Size() > maxLog {
		os.Truncate(dirs.Log(), 0)
	}
	label := Label(dirs)
	plist := filepath.Join(dirs.State, "server.plist")
	if err := os.WriteFile(plist, []byte(plistXML(label, exe, dirs)), 0o600); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	target := domain + "/" + label
	// The job stays loaded (idle) after the daemon exits. Never boot out a
	// running one: it may be a daemon another airs command just started, or one
	// that is still shutting down; Connect waits for its socket either way.
	if info, err := exec.Command("/bin/launchctl", "print", target).Output(); err == nil {
		if strings.Contains(string(info), "state = running") {
			return nil
		}
		exec.Command("/bin/launchctl", "bootout", target).Run() // reload with a fresh plist
	}
	out, err := exec.Command("/bin/launchctl", "bootstrap", domain, plist).CombinedOutput()
	if err == nil {
		return nil
	}
	// Another airs command may have loaded the job in the meantime.
	if exec.Command("/bin/launchctl", "print", target).Run() == nil {
		exec.Command("/bin/launchctl", "kickstart", target).Run() // no -k: never kills a running daemon
		return nil
	}
	appendLog(dirs, fmt.Sprintf("launchctl bootstrap failed (%v: %s); starting directly", err, out))
	return startDirect(exe, dirs)
}

func appendLog(dirs paths.Dirs, msg string) {
	f, err := os.OpenFile(dirs.Log(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	log.New(f, "", log.LstdFlags).Println(msg)
}

func startDirect(exe string, dirs paths.Dirs) error {
	logf, err := os.OpenFile(dirs.Log(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	devnull, _ := os.Open(os.DevNull)
	if devnull != nil {
		defer devnull.Close()
	}
	cmd := exec.Command(exe, "__serve")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func plistXML(label, exe string, dirs paths.Dirs) string {
	env := map[string]string{
		"HOME":            os.Getenv("HOME"),
		"XDG_CONFIG_HOME": filepath.Dir(dirs.Config),
		"XDG_STATE_HOME":  filepath.Dir(dirs.State),
		"PATH":            "/usr/bin:/bin:/usr/sbin:/sbin",
	}
	e := html.EscapeString
	s := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + e(label) + `</string>
  <key>ProgramArguments</key><array><string>` + e(exe) + `</string><string>__serve</string></array>
  <key>EnvironmentVariables</key><dict>
`
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "PATH"} {
		s += "    <key>" + k + "</key><string>" + e(env[k]) + "</string>\n"
	}
	s += `  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><false/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>` + e(dirs.Log()) + `</string>
  <key>StandardErrorPath</key><string>` + e(dirs.Log()) + `</string>
</dict>
</plist>
`
	return s
}
