// Package stage brings a node to a desired state. Each stage probes the node and
// changes only what differs, so rerunning tailk3s is setup and repair at the same time.
package stage

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
	"github.com/ude-p/tailk3s/internal/remote"
)

// No-ops when stdout is not a terminal, TERM is dumb, or NO_COLOR is set.
var (
	nodeHead  = color.New(color.FgCyan, color.Bold)
	stageName = color.New(color.FgCyan)
	converged = color.New(color.FgGreen)
	pending   = color.New(color.FgYellow)
)

// AdminUser is the account every node gets, and the login tailscale ssh uses.
const AdminUser = "tailk3s"

// Runner is what a stage can do to a node.
type Runner interface {
	RunRoot(cmd string) (remote.Result, error)
	WriteRootFile(path string, content []byte, mode, owner string) error
	Identity() remote.Identity
}

// Action is one change to a node.
type Action struct {
	Desc string
	Run  func(r Runner) error
}

// Stage plans the changes a node needs. An empty plan means it is already converged.
type Stage interface {
	Name() string
	Plan(r Runner) ([]Action, error)
}

// Apply runs stages in order and stops at the first failure. With dryRun it prints the
// plan and changes nothing.
func Apply(stages []Stage, r Runner, dryRun bool, out io.Writer) error {
	width := 0
	for _, s := range stages {
		width = max(width, len(s.Name()))
	}

	for _, s := range stages {
		actions, err := s.Plan(r)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name(), err)
		}
		name := stageName.Sprint(fmt.Sprintf("%-*s", width, s.Name()))

		if len(actions) == 0 {
			say(out, "  %s  %s\n", name, converged.Sprint("ok"))
			continue
		}
		for _, a := range actions {
			desc := a.Desc
			if dryRun {
				desc = pending.Sprint("would " + a.Desc)
			}
			say(out, "  %s  %s\n", name, desc)
			if dryRun {
				continue
			}
			if err := a.Run(r); err != nil {
				return fmt.Errorf("%s: %s: %w", s.Name(), a.Desc, err)
			}
		}
	}
	return nil
}

// Header names the node the stage lines below belong to.
func Header(out io.Writer, name, address, role string) {
	say(out, "\n%s  %s  %s\n", nodeHead.Sprint(name), address, role)
}

func say(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

type change struct {
	desc string
	done func(r Runner) (bool, error)
	do   func(r Runner) error
}

func plan(r Runner, changes []change) ([]Action, error) {
	var actions []Action
	for _, c := range changes {
		done, err := c.done(r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.desc, err)
		}
		if !done {
			actions = append(actions, Action{Desc: c.desc, Run: c.do})
		}
	}
	return actions, nil
}

// tailnetAddress is the node's tailnet address, which is the only one that stays
// reachable once the firewall closes the public ports.
func tailnetAddress(r Runner) (string, error) {
	ip, err := output(r, "tailscale ip -4")
	if err != nil {
		return "", err
	}
	if ip == "" {
		return "", errors.New("the node is not on the tailnet")
	}
	return ip, nil
}

func exitOK(r Runner, cmd string) (bool, error) {
	res, err := r.RunRoot(cmd)
	if err != nil {
		return false, err
	}
	return res.Code == 0, nil
}

func output(r Runner, cmd string) (string, error) {
	res, err := r.RunRoot(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

func root(r Runner, cmd string) error {
	res, err := r.RunRoot(cmd)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("exit %d: %s", res.Code, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func fileHas(r Runner, path, content string) (bool, error) {
	got, err := output(r, "cat "+remote.Quote(path)+" 2>/dev/null")
	if err != nil {
		return false, err
	}
	return got == strings.TrimSpace(content), nil
}
