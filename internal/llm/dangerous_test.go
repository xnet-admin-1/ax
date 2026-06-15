package llm_test

import (
	"testing"
	"github.com/xnet-admin-1/ax/internal/llm"
)

func TestIsDangerous(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"rm -rf /home/user", true},
		{"rm -rf /tmp/foo", false},
		{"ls -la", false},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"mkfs.ext4 /dev/sda1", true},
		{"chmod 777 /etc/passwd", true},
		{"kill -9 1234", true},
		{"git push --force", true},
		{"git reset --hard", true},
		{"echo 'DROP TABLE users'", true},
		{"echo hello > /etc/hosts", true},
		{"echo hello > /tmp/test", false},
		{"killall nginx", true},
	}
	for _, tt := range tests {
		got, reason := llm.IsDangerous(tt.cmd)
		if got != tt.want {
			t.Errorf("IsDangerous(%q) = %v (reason: %s), want %v", tt.cmd, got, reason, tt.want)
		}
	}
}
