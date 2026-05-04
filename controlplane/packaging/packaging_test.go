package packaging

import (
	"strings"
	"testing"
)

func TestInstallShellScript_HasSystemdBranch(t *testing.T) {
	if !strings.Contains(installShellScript, "systemctl daemon-reload") {
		t.Error("install.sh missing systemd branch")
	}
	if !strings.Contains(installShellScript, "systemctl enable --now okesu.service") {
		t.Error("install.sh missing systemctl enable")
	}
	if !strings.Contains(installShellScript, "[Unit]") {
		t.Error("install.sh missing systemd unit body")
	}
	if !strings.Contains(installShellScript, "ExecStart=/usr/local/bin/okesu s3-jobs") {
		t.Error("install.sh systemd unit ExecStart wrong")
	}
	if !strings.Contains(installShellScript, "nohup") {
		t.Error("install.sh missing nohup fallback")
	}
}
