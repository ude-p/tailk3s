package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/ude-p/tailk3s/internal/config"
	"github.com/ude-p/tailk3s/internal/remote"
	"github.com/ude-p/tailk3s/internal/stage"
	"github.com/ude-p/tailk3s/internal/tailapi"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	ctx := context.Background()
	switch os.Args[1] {
	case "up":
		err = run(ctx, os.Args[2:], false)
	case "plan":
		err = run(ctx, os.Args[2:], true)
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tailk3s:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tailk3s provisions nodes over SSH.

	usage:
		tailk3s up [flags]      set the nodes up
		tailk3s plan [flags]    show what up would change, and change nothing

	Either a cluster file or the flags below, not both:

		--config          cluster file holding the node list

	flags, for a single node:
		--host            address tailk3s connects to (required)
		--name            hostname tailk3s sets on the node (required)
		--role            server or agent (required)
		--init-user       provider login user (default $TAILK3S_INIT_USER)
		--init-password   provider login password (default $TAILK3S_INIT_PASSWORD)
		--init-key        provider login private key, used when set (default $TAILK3S_INIT_KEY)
		--ghcr-user       GitHub username for pulling private images (default $TAILK3S_GHCR_USER)
		--ghcr-token      GitHub token for pulling private images, empty skips the registry (default $TAILK3S_GHCR_TOKEN)
		--join            control plane to join: a name in the file, like b-cp, or a tag like
											tag:prod-cp-server for one tailk3s does not provision
		--k3s-version     k3s version to install (default $TAILK3S_K3S_VERSION, then "v1.33.5+k3s1")
		--disable         comma separated services to turn off, none by default

	environment, for this machine rather than the nodes:
		TAILK3S_API_KEY    Tailscale API access token: writes the policy, mints node keys
		TAILK3S_TS_STATE   directory for tailk3s's own node state

	cluster file, one block per node:

		node "cp1" {
			host = "92.205.183.108"
			role = "server"
		}

		node "w1" {
			host = "92.205.179.222"
			role = "agent"
			join = "cp1"
		}

		other keys: init_user, init_password, init_key, ghcr_username, ghcr_token,
		k3s_version, disable_services. Every one is optional. Values take env(), so a
		shared one reads as env("TAILK3S_INIT_PASSWORD") in each block instead of being
		repeated.

		a node wears the tailnet tag <name>-<role>, so nodes sharing a name share a tag.
		tailk3s writes those tags, and the rule letting it log in to the nodes, into the
		policy before it provisions anything, so the token above needs to be able to reach
		the tailnet's access controls and its keys.
	`)
	}

func run(ctx context.Context, args []string, dryRun bool) error {
	nodes, err := load(args)
	if err != nil {
		return err
	}

	api := tailapi.New(os.Getenv("TAILK3S_API_KEY"))

	// The nodes are only reachable once the policy names their tags, so this comes
	// before anything is provisioned.
	access := tailapi.Access{SSHUser: stage.AdminUser}
	for _, n := range nodes {
		access.NodeTags = append(access.NodeTags, n.TailscaleTags()...)
		if n.JoinIsTag() {
			// A control plane outside the config has to be named in the rule, but its
			// tag is not tailk3s's to own.
			access.JoinTags = append(access.JoinTags, n.Join)
		}
	}
	if dryRun {
		fmt.Printf("\nwould let %s reach %s\n", access.SSHUser, strings.Join(access.NodeTags, ", "))
	} else if err := api.Grant(ctx, access); err != nil {
		return err
	}

	// The orchestrator's key is only needed the first time; afterwards the state
	// directory carries the identity it was given.
	stateDir := os.Getenv("TAILK3S_TS_STATE")
	key := ""
	if _, err := os.Stat(filepath.Join(stateDir, "tailscaled.state")); err != nil {
		if key, err = api.MintKey(ctx, []string{tailapi.OwnerTag}); err != nil {
			return err
		}
	}
	// tailk3s's own node on the tailnet, separate from the nodes being provisioned.
	tailnet, err := remote.JoinTailnet(ctx, stateDir, key, []string{tailapi.OwnerTag})
	if err != nil {
		return err
	}
	defer func() { _ = tailnet.Close() }()

	for _, n := range nodes {
		if err := setUpNode(ctx, tailnet, api, n, dryRun); err != nil {
			return err
		}
	}
	return nil
}

func load(args []string) ([]config.Node, error) {
	fs := flag.NewFlagSet("tailk3s", flag.ExitOnError)
	path := fs.String("config", "", "cluster file holding the node list")
	fromFlags := config.Node{}
	fs.StringVar(&fromFlags.Host, "host", "", "address tailk3s connects to")
	fs.StringVar(&fromFlags.Name, "name", "", "hostname tailk3s sets on the node")
	fs.StringVar(&fromFlags.Role, "role", "", "server or agent")
	fs.StringVar(&fromFlags.Join, "join", os.Getenv("TAILK3S_JOIN"), "control plane to join: a name in the file, or a tag: for one outside it")
	fs.StringVar(&fromFlags.K3sVersion, "k3s-version", os.Getenv("TAILK3S_K3S_VERSION"), "k3s version to install")
	fs.StringVar(&fromFlags.InitUser, "init-user", os.Getenv("TAILK3S_INIT_USER"), "provider login user")
	fs.StringVar(&fromFlags.InitPassword, "init-password", os.Getenv("TAILK3S_INIT_PASSWORD"), "provider login password")
	fs.StringVar(&fromFlags.InitKey, "init-key", os.Getenv("TAILK3S_INIT_KEY"), "provider login private key, used when set")
	fs.StringVar(&fromFlags.GHCRUsername, "ghcr-user", os.Getenv("TAILK3S_GHCR_USER"), "GitHub username for pulling private images")
	fs.StringVar(&fromFlags.GHCRToken, "ghcr-token", os.Getenv("TAILK3S_GHCR_TOKEN"), "GitHub token for pulling private images")
	var disable string
	fs.StringVar(&disable, "disable", os.Getenv("TAILK3S_DISABLE"), "comma separated services to turn off")
	_ = fs.Parse(args) // ExitOnError already printed usage and exited on a bad flag

	for name := range strings.SplitSeq(disable, ",") {
		if name = strings.TrimSpace(name); name != "" {
			fromFlags.DisableServices = append(fromFlags.DisableServices, name)
		}
	}

	nodes := []config.Node{fromFlags}
	if *path != "" {
		if err := fileOnly(fs); err != nil {
			return nil, err
		}
		fileNodes, err := config.FromFile(*path)
		if err != nil {
			return nil, err
		}
		nodes = fileNodes
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("%s holds no nodes", *path)
	}

	seen := make(map[string]string, len(nodes))
	servers := make(map[string]bool, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		n.ApplyDefaults()
		if err := n.Validate(); err != nil {
			return nil, err
		}
		if first, ok := seen[n.Host]; ok {
			return nil, fmt.Errorf("nodes %s and %s share host %s", first, n.Name, n.Host)
		}
		seen[n.Host] = n.Name
		if n.Role == "server" {
			servers[n.Name] = true
		}
	}

	// A bare name has to be a server here. A tag names a control plane outside the file,
	// which is assumed to exist.
	for _, n := range nodes {
		if n.Join != "" && !n.JoinIsTag() && !servers[n.Join] {
			return nil, fmt.Errorf("node %s joins %s, but the file has no server by that name, and it is not a tag", n.Name, n.Join)
		}
	}

	// A node that joins reads its control plane off the tailnet, so the nodes that found
	// clusters have to be provisioned first.
	var founders, joiners []config.Node
	for _, n := range nodes {
		if n.Join == "" {
			founders = append(founders, n)
		} else {
			joiners = append(joiners, n)
		}
	}
	return append(founders, joiners...), nil
}

// fileOnly rejects flags given on the command line alongside --config, so a typo
// cannot be silently ignored.
func fileOnly(fs *flag.FlagSet) error {
	var given []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "config" {
			given = append(given, "--"+f.Name)
		}
	})
	if len(given) == 0 {
		return nil
	}
	return fmt.Errorf("--config reads the node list, so drop %s", strings.Join(given, " "))
}

func setUpNode(ctx context.Context, tailnet *remote.Tailnet, api *tailapi.Client, n config.Node, dryRun bool) error {
	stage.Header(os.Stdout, n.Name, n.Address(), n.Role)

	address, err := tailnet.Address(ctx, n.NodeName())
	if err != nil {
		return err
	}

	// The key is single use, so it is minted only for a node that still has to join.
	key := ""
	if address == "" {
		if n.InitUser == "" {
			return fmt.Errorf("%s is not on the tailnet, and without an init_user there is no way in", n.Name)
		}
		if !dryRun {
			if key, err = api.MintKey(ctx, n.TailscaleTags()); err != nil {
				return err
			}
		}
	}

	// Before the tailnet, the provider login over the public address is the only way in.
	if address == "" {
		provider, err := remote.Dial(ctx, n.Address(), remote.LocalDial, n.InitIdentity())
		if err != nil {
			return err
		}
		defer func() { _ = provider.Close() }()

		if err := stage.Apply(bootstrapStages(n, key), provider, dryRun, os.Stdout); err != nil {
			return err
		}
		if dryRun {
			fmt.Println("  ! plan stops here: the rest needs the node on the tailnet")
			return nil
		}
		if address, err = tailnet.Address(ctx, n.NodeName()); err != nil {
			return err
		}
		if address == "" {
			return fmt.Errorf("%s joined the tailnet but is not answering on it", n.Name)
		}
	}

	// The orchestrator reads the token, so a joining node never has to reach the control
	// plane itself, only the cluster's API.
	var join stage.Join
	if n.Join != "" {
		if join, err = stage.FetchJoin(ctx, tailnet, n.JoinTag(), stage.AdminUser); err != nil {
			return err
		}
	}

	// Tailscale SSH authorizes by tailnet identity, so this login carries nothing.
	login := remote.Identity{Label: "tailscale ssh", User: stage.AdminUser}
	client, err := remote.Dial(ctx, net.JoinHostPort(address, "22"), tailnet.Dial, login)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return stage.Apply(mainStages(n, join), client, dryRun, os.Stdout)
}

// bootstrapStages are what puts a node on the tailnet, so they run over the provider
// login.
func bootstrapStages(n config.Node, authKey string) []stage.Stage {
	stages := []stage.Stage{&stage.Base{Hostname: n.Name}}
	if len(n.DisableServices) > 0 {
		stages = append(stages, &stage.Services{Disable: n.DisableServices})
	}
	tailscale := &stage.Tailscale{AuthKey: authKey, Tags: n.TailscaleTags(), Hostname: n.NodeName()}
	return append(stages, tailscale)
}

// mainStages are everything that needs the node on the tailnet, so they run over
// tailscale ssh.
func mainStages(n config.Node, join stage.Join) []stage.Stage {
	// The firewall closes the public ports that the tailnet replaces, so it goes first.
	stages := []stage.Stage{&stage.Firewall{}}
	if n.GHCRToken != "" {
		stages = append(stages, &stage.Registry{Role: n.Role, Username: n.GHCRUsername, Token: n.GHCRToken})
	}
	k3s := &stage.K3s{
		Role:     n.Role,
		Version:  n.K3sVersion,
		Hostname: n.Name,
		NodeName: n.NodeName(),
		Host:     n.HostOnly(),
		Join:     join,
	}
	stages = append(stages, k3s)
	if n.Role == "server" {
		stages = append(stages, &stage.Kubeconfig{Node: n.NodeName()})
	}
	stages = append(stages, &stage.SSHConfig{Node: n.NodeName()})

	// Purge deletes the provider login, which is the fallback if anything else goes
	// wrong, so it only runs once everything else has worked.
	return append(stages, &stage.Purge{InitUser: n.InitUser})
}
