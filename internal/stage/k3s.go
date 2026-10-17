package stage

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ude-p/tailk3s/internal/remote"
)

const k3sConfig = "/etc/rancher/k3s/config.yaml"

// K3s installs the node as a control plane or a worker. The node is addressed by its
// tailnet address and flannel runs over tailscale0, so the tailnet has to be up first.
type K3s struct {
	Role     string // server or agent
	Version  string
	Hostname string // what base set on the node, and a tls-san entry
	NodeName string // what the cluster calls the node
	Host     string // the address tailk3s dials, for node-external-ip
	Join     Join   // empty for the control plane that founds the cluster
}

func (k *K3s) Name() string { return "k3s" }

func (k *K3s) Plan(r Runner) ([]Action, error) {
	tailnetIP, err := tailnetAddress(r)
	if err != nil {
		return nil, err
	}

	var join string
	if len(k.Join.Servers) > 0 {
		join = joinSettings(k.Join)
	}

	unit := k3sUnit(k.Role)
	config := k.config(tailnetIP, join)

	writeConfig := change{
		desc: "write " + k3sConfig,
		done: func(r Runner) (bool, error) { return fileHas(r, k3sConfig, config) },
		do: func(r Runner) error {
			if err := r.WriteRootFile(k3sConfig, []byte(config), "0600", ""); err != nil {
				return err
			}
			return restartIfRunning(r, unit)
		},
	}

	installK3s := change{
		desc: "install k3s " + k.Version,
		done: func(r Runner) (bool, error) {
			got, err := output(r, "k3s --version")
			return strings.Contains(got, k.Version), err
		},
		do: func(r Runner) error {
			return root(r, "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="+
				remote.Quote(k.Version)+" sh -s - "+k.Role)
		},
	}

	start := change{
		desc: "start " + unit,
		done: func(r Runner) (bool, error) { return exitOK(r, "systemctl is-active "+unit) },
		do:   func(r Runner) error { return root(r, "systemctl enable --now "+unit) },
	}

	changes := []change{writeConfig, installK3s, start}
	if k.Role == "server" {
		changes = append(changes, change{
			desc: "wait for the API",
			done: func(r Runner) (bool, error) { return exitOK(r, k3sReady) },
			do:   func(r Runner) error { return waitFor(r, k3sReady, 60) },
		})
	}
	return plan(r, changes)
}

const k3sReady = "k3s kubectl get --raw=/readyz >/dev/null 2>&1"

func (k *K3s) config(tailnetIP, join string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "node-name: %q\n", k.NodeName)
	fmt.Fprintf(&b, "node-ip: %q\n", tailnetIP)
	fmt.Fprintf(&b, "node-external-ip: %q\n", k.Host)
	b.WriteString("flannel-iface: tailscale0\n")

	if k.Role == "agent" {
		b.WriteString(join)
		return b.String()
	}

	b.WriteString("write-kubeconfig-mode: \"0600\"\n")

	// Only the first control plane creates the cluster, and that is where the
	// datastore is chosen: embedded etcd, because SQLite cannot have a second server.
	if len(k.Join.Servers) == 0 {
		b.WriteString("cluster-init: true\n")
	}
	fmt.Fprintf(&b, "advertise-address: %q\n", tailnetIP)
	b.WriteString("tls-san:\n")
	fmt.Fprintf(&b, "  - %s\n", tailnetIP)
	fmt.Fprintf(&b, "  - %s\n", k.Hostname)
	b.WriteString(join)
	return b.String()
}

// Join is where a joining node finds its control plane, and the token that lets it in.
type Join struct {
	Servers []string // tailnet addresses of every control plane wearing the tag
	Token   string
}

// FetchJoin reads the join token off a control plane wearing tag. The orchestrator does
// this, not the joining node, so a node only needs to reach the cluster's API and never
// another node's ssh.
func FetchJoin(ctx context.Context, tailnet *remote.Tailnet, tag, user string) (Join, error) {
	servers, err := tailnet.Peers(ctx, tag)
	if err != nil {
		return Join{}, err
	}
	if len(servers) == 0 {
		return Join{}, fmt.Errorf("no control plane wearing %s is online", tag)
	}

	login := remote.Identity{Label: "tailscale ssh", User: user}
	client, err := remote.Dial(ctx, net.JoinHostPort(servers[0], "22"), tailnet.Dial, login)
	if err != nil {
		return Join{}, err
	}
	defer func() { _ = client.Close() }()

	res, err := client.RunRoot("cat " + nodeTokenPath)
	if err != nil {
		return Join{}, err
	}
	if res.Code != 0 {
		return Join{}, fmt.Errorf("read the join token from %s: %s", servers[0], strings.TrimSpace(res.Stderr))
	}
	return Join{Servers: servers, Token: strings.TrimSpace(res.Stdout)}, nil
}

// nodeTokenPath is where k3s leaves the token a joining node needs.
const nodeTokenPath = "/var/lib/rancher/k3s/server/node-token"

// joinSettings points a joining node at every control plane found, so it is not tied to
// one being up.
func joinSettings(j Join) string {
	servers := make([]string, 0, len(j.Servers))
	for _, address := range j.Servers {
		servers = append(servers, "https://"+net.JoinHostPort(address, "6443"))
	}
	return fmt.Sprintf("server: %q\ntoken: %q\n", strings.Join(servers, ","), j.Token)
}

func k3sUnit(role string) string {
	if role == "agent" {
		return "k3s-agent"
	}
	return "k3s"
}

// restartIfRunning applies a config change: k3s keeps the settings it started with,
// so a rewrite only takes effect on a restart, and on a first install the script
// starts it instead.
func restartIfRunning(r Runner, unit string) error {
	running, err := exitOK(r, "systemctl is-active "+unit)
	if err != nil || !running {
		return err
	}
	return root(r, "systemctl restart "+unit)
}

// waitFor polls cmd until it succeeds. k3s takes a while to come up, and nothing on
// the node tells us when it is done.
func waitFor(r Runner, cmd string, tries int) error {
	for range tries {
		ok, err := exitOK(r, cmd)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("still not ready after %d checks", tries)
}
