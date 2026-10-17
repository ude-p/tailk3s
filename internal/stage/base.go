package stage

import (
	"strings"

	"github.com/ude-p/tailk3s/internal/remote"
)

// basePackages covers what Longhorn needs on every node: open-iscsi for its volumes, and
// nfs-common for RWX. k3s bundles its own iptables, socat and containerd.
var basePackages = []string{"openssh-server", "sudo", "curl", "open-iscsi", "nfs-common"}

// Base prepares a fresh node: the admin account, the hostname, and the packages later
// stages need. The admin account is not tailk3s's login — tailscale ssh is — so no key is
// installed for it.
type Base struct {
	Hostname string
}

func (b *Base) Name() string { return "base" }

func (b *Base) Plan(r Runner) ([]Action, error) {
	admin := AdminUser
	sudoersFile := "/etc/sudoers.d/" + admin
	sudoersRule := admin + " ALL=(ALL) NOPASSWD:ALL\n"
	hostsLine := "127.0.1.1 " + b.Hostname
	installed := "for p in " + strings.Join(basePackages, " ") +
		`; do dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'install ok installed' || echo "$p"; done`

	installPackages := change{
		desc: "install " + strings.Join(basePackages, " "),
		done: func(r Runner) (bool, error) {
			missing, err := output(r, installed)
			return missing == "", err
		},
		do: func(r Runner) error {
			return root(r, "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y "+
				strings.Join(basePackages, " "))
		},
	}

	startIscsid := change{
		desc: "start iscsid",
		done: func(r Runner) (bool, error) {
			return exitOK(r, "systemctl is-enabled iscsid >/dev/null 2>&1 && systemctl is-active iscsid >/dev/null 2>&1")
		},
		do: func(r Runner) error { return root(r, "systemctl enable --now iscsid") },
	}

	setHostname := change{
		desc: "set hostname to " + b.Hostname,
		done: func(r Runner) (bool, error) {
			got, err := output(r, "hostname")
			return got == b.Hostname, err
		},
		do: func(r Runner) error { return root(r, "hostnamectl set-hostname "+remote.Quote(b.Hostname)) },
	}

	addToHosts := change{
		desc: "add " + b.Hostname + " to /etc/hosts",
		done: func(r Runner) (bool, error) { return exitOK(r, "grep -qxF "+remote.Quote(hostsLine)+" /etc/hosts") },
		do:   func(r Runner) error { return root(r, "echo "+remote.Quote(hostsLine)+" >> /etc/hosts") },
	}

	createUser := change{
		desc: "create user " + admin,
		done: func(r Runner) (bool, error) { return exitOK(r, "id -u "+admin+" >/dev/null 2>&1") },
		do:   func(r Runner) error { return root(r, "useradd --create-home --shell /bin/bash --groups sudo "+admin) },
	}

	addToSudoGroup := change{
		desc: "add " + admin + " to the sudo group",
		done: func(r Runner) (bool, error) { return exitOK(r, "id -nG "+admin+" | grep -qw sudo") },
		do:   func(r Runner) error { return root(r, "usermod -aG sudo "+admin) },
	}

	// The rule is checked before it is installed: a broken sudoers file takes sudo
	// away on the node, which remotely means being locked out.
	grantSudo := change{
		desc: "grant passwordless sudo to " + admin,
		done: func(r Runner) (bool, error) { return fileHas(r, sudoersFile, sudoersRule) },
		do: func(r Runner) error {
			const tmp = "/root/.tailk3s-sudoers"
			if err := r.WriteRootFile(tmp, []byte(sudoersRule), "0600", ""); err != nil {
				return err
			}
			return root(r, "visudo -cf "+tmp+
				" && install -m 0440 -o root -g root "+tmp+" "+remote.Quote(sudoersFile)+
				" && rm -f "+tmp)
		},
	}

	return plan(r, []change{
		installPackages,
		startIscsid,
		setHostname,
		addToHosts,
		createUser,
		addToSudoGroup,
		grantSudo,
	})
}
