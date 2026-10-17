package stage

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ude-p/tailk3s/internal/remote"
)

// Tailscale joins the node to the tailnet and serves the SSH that tailk3s logs in with.
type Tailscale struct {
	AuthKey  string
	Tags     []string
	Hostname string // how tailk3s finds the node on the tailnet
}

func (t *Tailscale) Name() string { return "tailscale" }

func (t *Tailscale) Plan(r Runner) ([]Action, error) {
	install := change{
		desc: "install tailscale",
		done: func(r Runner) (bool, error) { return exitOK(r, "command -v tailscale") },
		do: func(r Runner) error {
			return root(r, "curl -fsSL https://tailscale.com/install.sh | sh")
		},
	}

	// A single-use auth key is spent on every join, so this check has to be right.
	join := change{
		desc: "join the tailnet as " + strings.Join(t.Tags, ","),
		done: func(r Runner) (bool, error) {
			status, err := tailscaleStatusOf(r)
			if err != nil || len(status.Self.TailscaleIPs) == 0 {
				return false, err
			}
			for _, tag := range t.Tags {
				if !slices.Contains(status.Self.Tags, tag) {
					return false, nil
				}
			}
			return true, nil
		},
		do: func(r Runner) error {
			cmd := "tailscale up --ssh --hostname=" + remote.Quote(t.Hostname) +
				" --authkey=" + remote.Quote(t.AuthKey)
			if len(t.Tags) > 0 {
				cmd += " --advertise-tags=" + remote.Quote(strings.Join(t.Tags, ","))
			}
			return root(r, cmd)
		},
	}

	// A node joined before tailk3s needed tailscale ssh is switched over, not re-authed.
	serveSSH := change{
		desc: "serve tailscale ssh",
		done: func(r Runner) (bool, error) { return sshEnabled(r) },
		do: func(r Runner) error {
			return root(r, "tailscale set --ssh --hostname="+remote.Quote(t.Hostname))
		},
	}

	return plan(r, []change{install, join, serveSSH})
}

// sshEnabled reads the pref `tailscale set --ssh` writes, which is not in the status.
func sshEnabled(r Runner) (bool, error) {
	res, err := r.RunRoot("tailscale debug prefs")
	if err != nil {
		return false, err
	}
	// Nothing to read before the install action has run.
	if res.Code != 0 {
		return false, nil
	}
	var prefs struct{ RunSSH bool }
	if err := json.Unmarshal([]byte(res.Stdout), &prefs); err != nil {
		return false, fmt.Errorf("read tailscale prefs: %w", err)
	}
	return prefs.RunSSH, nil
}

type tailscaleStatus struct {
	Self struct {
		TailscaleIPs []string
		Tags         []string
	}
}

// tailscaleStatusOf asks the node's own tailscaled. An empty status means it is not
// running there yet.
func tailscaleStatusOf(r Runner) (tailscaleStatus, error) {
	raw, err := output(r, "tailscale status --json")
	if err != nil {
		return tailscaleStatus{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return tailscaleStatus{}, nil
	}
	var status tailscaleStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return tailscaleStatus{}, fmt.Errorf("read tailscale status: %w", err)
	}
	return status, nil
}
