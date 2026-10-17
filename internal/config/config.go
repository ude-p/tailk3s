package config

import (
	"cmp"
	"errors"
	"net"
	"strings"

	"github.com/ude-p/tailk3s/internal/remote"
	"github.com/ude-p/tailk3s/internal/shared"
)

const (
	DefaultK3sVersion = "v1.33.5+k3s1"
)

// Node is one machine, and one block in the cluster file.
type Node struct {
	Name string `hcl:"name,label"`
	Host string `hcl:"host"`
	Role string `hcl:"role"`
	Join string `hcl:"join,optional"`

	InitUser     string `hcl:"init_user,optional"` // used for the first connection only
	InitPassword string `hcl:"init_password,optional"`
	InitKey      string `hcl:"init_key,optional"`

	GHCRUsername string `hcl:"ghcr_username,optional"`
	GHCRToken    string `hcl:"ghcr_token,optional"`

	K3sVersion      string   `hcl:"k3s_version,optional"`
	DisableServices []string `hcl:"disable_services,optional"`
}

// ApplyDefaults fills in the settings that have a sensible default.
func (n *Node) ApplyDefaults() {
	n.K3sVersion = cmp.Or(n.K3sVersion, DefaultK3sVersion)
}

// Validate reports every problem at once, so run ApplyDefaults first.
func (n Node) Validate() error {
	at := cmp.Or(n.Name, "node")

	var errs []error
	if n.Name == "" {
		errs = append(errs, errors.New(at+": name is required"))
	}
	if n.Host == "" {
		errs = append(errs, errors.New(at+": host is required"))
	}
	if n.Role != "server" && n.Role != "agent" {
		errs = append(errs, errors.New(at+": role must be server or agent"))
	}
	if n.Role == "agent" && n.Join == "" {
		errs = append(errs, errors.New(at+": join is required for an agent"))
	}
	return errors.Join(errs...)
}

// TailscaleTags is the identity the node wears on the tailnet, and how the policy names
// it. Nodes sharing a name share a tag, and the role keeps an agent from being taken for
// a control plane.
func (n Node) TailscaleTags() []string {
	return []string{"tag:" + n.Name + "-" + n.Role}
}

// JoinTag is the tag of the control plane this node joins.
func (n Node) JoinTag() string {
	if n.Join == "" || n.JoinIsTag() {
		return n.Join
	}
	return "tag:" + n.Join + "-server"
}

// JoinIsTag reports whether Join names a control plane outside the cluster file, which
// is how one that tailk3s does not provision is reached.
func (n Node) JoinIsTag() bool {
	return strings.HasPrefix(n.Join, "tag:")
}

// Address is where to reach the node, with the port defaulted to 22.
func (n Node) Address() string {
	if _, _, err := net.SplitHostPort(n.Host); err == nil {
		return n.Host
	}
	return net.JoinHostPort(n.Host, "22")
}

// HostOnly is the host without a port, for settings that take an address alone.
func (n Node) HostOnly() string {
	host, _, err := net.SplitHostPort(n.Host)
	if err != nil {
		return n.Host
	}
	return host
}

// NodeName is what the cluster calls the node, so the same name can be reused across
// nodes.
func (n Node) NodeName() string {
	return n.Name + "-" + shared.ShortHash(n.Host)
}

// InitIdentity is the provider login, the only way in before the node is on the tailnet.
func (n Node) InitIdentity() remote.Identity {
	if n.InitUser == "" {
		return remote.Identity{}
	}
	return remote.Identity{
		Label:    "provider login",
		User:     n.InitUser,
		KeyPath:  n.InitKey,
		Password: n.InitPassword,
	}
}
