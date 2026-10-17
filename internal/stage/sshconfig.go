package stage

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// includeLine is what makes ssh read the files tailk3s writes. It has to be first,
// because ssh uses the first matching Host block and an earlier Host * would win.
const includeLine = "Include config.d/*"

// SSHConfig writes an ssh alias for the node, in its own file under ~/.ssh/config.d,
// so tailk3s never rewrites Host blocks you wrote yourself. The alias points at the
// tailnet address, which is what stays reachable.
type SSHConfig struct {
	Node string
}

func (s *SSHConfig) Name() string { return "ssh" }

func (s *SSHConfig) Plan(r Runner) ([]Action, error) {
	address, err := tailnetAddress(r)
	if err != nil {
		return nil, err
	}

	file, err := localPath(".ssh", "config.d", s.Node)
	if err != nil {
		return nil, err
	}
	config, err := localPath(".ssh", "config")
	if err != nil {
		return nil, err
	}
	block := fmt.Sprintf("Host %s\n  HostName %s\n  User %s\n", s.Node, address, AdminUser)

	include := change{
		desc: "include ~/.ssh/config.d from ~/.ssh/config",
		done: func(r Runner) (bool, error) { return includesDir(config) },
		do:   func(r Runner) error { return addInclude(config) },
	}

	alias := change{
		desc: "write ~/.ssh/config.d/" + s.Node,
		done: func(r Runner) (bool, error) { return localFileIs(file, block) },
		do:   func(r Runner) error { return writeLocal(file, block) },
	}

	return plan(r, []change{include, alias})
}

func includesDir(config string) (bool, error) {
	b, err := os.ReadFile(config)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.Contains(string(b), includeLine), nil
}

func addInclude(config string) error {
	b, err := os.ReadFile(config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeLocal(config, includeLine+"\n"+string(b))
}
