package stage

import (
	"fmt"
	"strings"
)

var publicPorts = []string{"80", "443"}

// Firewall closes the node to the internet except for publicPorts. It needs the node on
// a tailnet first, because after this the tailnet is the only way in.
type Firewall struct{}

func (f *Firewall) Name() string { return "firewall" }

func (f *Firewall) Plan(r Runner) ([]Action, error) {
	// Fail here rather than partway through, when the join left no tailnet address.
	if _, err := tailnetAddress(r); err != nil {
		return nil, err
	}

	install := change{
		desc: "install ufw",
		done: func(r Runner) (bool, error) { return exitOK(r, "command -v ufw") },
		do:   func(r Runner) error { return root(r, "DEBIAN_FRONTEND=noninteractive apt-get install -y ufw") },
	}

	var applied strings.Builder
	applied.WriteString("ufw status | grep -q '^Status: active' && ufw status | grep -q tailscale0")
	for _, port := range publicPorts {
		fmt.Fprintf(&applied, " && ufw status | grep -q '%s/tcp'", port)
	}

	closeIncoming := change{
		desc: "deny the internet except " + strings.Join(publicPorts, " and "),
		done: func(r Runner) (bool, error) { return exitOK(r, applied.String()) },
		do: func(r Runner) error {
			cmds := []string{
				// Pod traffic crosses this node on its way to a pod elsewhere, and ufw's
				// default forward policy drops exactly that.
				`sed -i 's/^DEFAULT_FORWARD_POLICY=.*/DEFAULT_FORWARD_POLICY="ACCEPT"/' /etc/default/ufw`,
				`grep -q '^DEFAULT_FORWARD_POLICY="ACCEPT"' /etc/default/ufw`,
				// Rules first, so enabling cannot cut the connection this runs over.
				"ufw default deny incoming",
				"ufw default allow outgoing",
				"ufw allow in on tailscale0",
			}
			for _, port := range publicPorts {
				cmds = append(cmds, "ufw allow "+port+"/tcp")
			}
			cmds = append(cmds, "ufw --force enable")
			return root(r, strings.Join(cmds, " && "))
		},
	}

	return plan(r, []change{install, closeIncoming})
}
