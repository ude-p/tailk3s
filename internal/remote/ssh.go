package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Identity struct {
	Label    string // names the identity in errors, e.g. "provider login"
	User     string
	KeyPath  string // private key, used instead of a password when set
	Password string
}

// A non-zero Code is a result, not a failure.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Dialer opens a connection to a node: the local network for one that is still off the
// tailnet, and the tailnet for one that is on it.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// LocalDial reaches an address over the ordinary network.
func LocalDial(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

type SSH struct {
	ctx      context.Context
	addr     string
	dial     Dialer
	current  Identity
	client   *ssh.Client
	hostKeys ssh.HostKeyCallback
}

// Dial connects to addr through dial, trying each identity in order and keeping the
// first that works.
func Dial(ctx context.Context, addr string, dial Dialer, ids ...Identity) (*SSH, error) {
	hostKeys, err := hostKeyCheck()
	if err != nil {
		return nil, err
	}
	s := &SSH{ctx: ctx, addr: addr, dial: dial, hostKeys: hostKeys}
	var errs []error
	for _, id := range ids {
		err := s.connect(id)
		if err == nil {
			return s, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("connect to %s: no login to try", addr)
	}
	return nil, fmt.Errorf("connect to %s: %w", addr, errors.Join(errs...))
}

func (s *SSH) Identity() Identity { return s.current }

func (s *SSH) Close() error { return s.client.Close() }

func (s *SSH) RunRoot(cmd string) (Result, error) {
	// The provider login is not in sudoers, so sudo reads the password from stdin.
	if s.current.Password != "" {
		return s.run("sudo -S -p '' bash -c "+Quote(cmd), s.current.Password+"\n")
	}
	return s.run("sudo -n bash -c "+Quote(cmd), "")
}

// WriteRootFile writes content to path as root. owner is "user:group", or empty for root.
func (s *SSH) WriteRootFile(path string, content []byte, mode, owner string) error {
	// base64 survives the shell; umask 077 keeps the file unreadable while written.
	cmd := fmt.Sprintf("mkdir -p %s && printf %%s %s | base64 -d | (umask 077 && cat > %s) && chmod %s %s",
		Quote(filepath.Dir(path)), Quote(base64.StdEncoding.EncodeToString(content)), Quote(path), mode, Quote(path))
	if owner != "" {
		cmd += " && chown " + owner + " " + Quote(path)
	}
	res, err := s.RunRoot(cmd)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("write %s: %s", path, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (s *SSH) connect(id Identity) error {
	cfg := &ssh.ClientConfig{
		User:            id.User,
		HostKeyCallback: s.hostKeys,
		Timeout:         15 * time.Second,
	}
	if id.KeyPath != "" {
		pem, err := os.ReadFile(id.KeyPath)
		if err != nil {
			return fmt.Errorf("%s: %w", id.Label, err)
		}
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return fmt.Errorf("%s: %w", id.Label, err)
		}
		cfg.Auth = append(cfg.Auth, ssh.PublicKeys(signer))
	}
	if id.Password != "" {
		// Both methods, because some nodes only offer keyboard-interactive.
		cfg.Auth = append(cfg.Auth, ssh.Password(id.Password))
		cfg.Auth = append(cfg.Auth, ssh.KeyboardInteractive(func(_, _ string, prompts []string, _ []bool) ([]string, error) {
			return slices.Repeat([]string{id.Password}, len(prompts)), nil
		}))
	}
	// No credentials is a valid login here: the client offers "none" first, and
	// Tailscale SSH accepts it because the tailnet identity is the credential.
	conn, err := s.dial(s.ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("%s: %w", id.Label, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, s.addr, cfg)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%s: %w", id.Label, err)
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	s.client, s.current = ssh.NewClient(sshConn, chans, reqs), id
	return nil
}

// run takes stdin so sudo can read a password without a terminal.
func (s *SSH) run(cmd, stdin string) (Result, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = sess.Close() }()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	err = sess.Run(cmd)

	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if exit, ok := errors.AsType[*ssh.ExitError](err); ok {
		res.Code = exit.ExitStatus()
		return res, nil
	}
	return res, err
}

// hostKeyCheck gives OpenSSH's accept-new behaviour: known hosts are verified, new
// ones are recorded, and a changed key is refused.
func hostKeyCheck() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// knownhosts.New needs the file to exist, and opening it must not truncate it.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}
	return func(host string, addr net.Addr, key ssh.PublicKey) error {
		err := verify(host, addr, key)
		// A known host returns nil. An unknown host reports no keys to compare
		// against; a changed key reports the ones we already have.
		if unknown, ok := errors.AsType[*knownhosts.KeyError](err); ok && len(unknown.Want) == 0 {
			return rememberHost(path, host, key)
		}
		return err
	}, nil
}

func rememberHost(path, host string, key ssh.PublicKey) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(host)}, key))
	return err
}

// Quote keeps s intact as one argument for the remote shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
