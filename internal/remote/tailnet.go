package remote

import (
	"context"
	"fmt"
	"log"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/ude-p/tailk3s/internal/shared"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

// Tailnet is this machine's own node on the tailnet, so reaching the nodes needs no
// system tailscaled and no tailscale command.
type Tailnet struct {
	server *tsnet.Server
}

// JoinTailnet brings the embedded node up. The auth key is only needed the first time;
// after that the node state in dir carries its identity.
func JoinTailnet(ctx context.Context, dir, authKey string, tags []string) (*Tailnet, error) {
	name, err := nodeName()
	if err != nil {
		return nil, err
	}
	server := &tsnet.Server{
		Dir:           dir,
		Hostname:      name,
		AuthKey:       authKey,
		AdvertiseTags: tags,
		UserLogf:      log.Printf,
	}
	if _, err := server.Up(ctx); err != nil {
		return nil, err
	}
	return &Tailnet{server: server}, nil
}

// nodeName is what the tailnet calls this machine, carrying a digest of the address
// rather than the address itself.
func nodeName() (string, error) {
	// The UDP dial picks the interface the route would use without sending anything.
	conn, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return "", fmt.Errorf("find this machine's address: %w", err)
	}
	_ = conn.Close()
	ip, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return "", err
	}
	return "tailk3s-" + shared.ShortHash(ip), nil
}

func (t *Tailnet) Close() error { return t.server.Close() }

func (t *Tailnet) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return t.server.Dial(ctx, network, address)
}

// Address returns the node's tailnet address, or "" when it does not appear on the
// tailnet within peerWait.
func (t *Tailnet) Address(ctx context.Context, hostname string) (string, error) {
	return waitForMatch(ctx, func() (string, bool, error) {
		status, err := t.status(ctx)
		if err != nil {
			return "", false, err
		}
		for _, peer := range status.Peer {
			if online(peer) && strings.EqualFold(peer.HostName, hostname) {
				return peer.TailscaleIPs[0].String(), true, nil
			}
		}
		return "", false, nil
	})
}

// Peers returns the addresses of the online peers wearing tag, which is how a control
// plane is found without naming the machine.
func (t *Tailnet) Peers(ctx context.Context, tag string) ([]string, error) {
	return waitForMatch(ctx, func() ([]string, bool, error) {
		status, err := t.status(ctx)
		if err != nil {
			return nil, false, err
		}
		var found []string
		for _, peer := range status.Peer {
			if online(peer) && peer.Tags != nil && slices.Contains(peer.Tags.AsSlice(), tag) {
				found = append(found, peer.TailscaleIPs[0].String())
			}
		}
		slices.Sort(found)
		return found, len(found) > 0, nil
	})
}

// peerWait bounds the wait for a peer that may just have joined.
const peerWait = 3 * time.Second

// waitForMatch retries get until it finds a match, since the netmap arrives a moment
// after the connection is up.
func waitForMatch[T any](ctx context.Context, get func() (T, bool, error)) (T, error) {
	deadline := time.Now().Add(peerWait)
	for {
		v, found, err := get()
		if err != nil || found || time.Now().After(deadline) {
			return v, err
		}
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (t *Tailnet) status(ctx context.Context) (*ipnstate.Status, error) {
	client, err := t.server.LocalClient()
	if err != nil {
		return nil, err
	}
	return client.Status(ctx)
}

func online(peer *ipnstate.PeerStatus) bool {
	return peer.Online && len(peer.TailscaleIPs) > 0
}
