package stage

import "github.com/ude-p/tailk3s/internal/remote"

// Purge removes the provider login, which is the account with a password that tailk3s
// no longer needs. Every other account on the node is left alone, so tailk3s can be run
// against a machine that is already in use.
type Purge struct {
	InitUser string
}

func (p *Purge) Name() string { return "purge" }

func (p *Purge) Plan(r Runner) ([]Action, error) {
	if p.InitUser == "" || p.InitUser == AdminUser {
		return nil, nil
	}

	uid, err := output(r, "id -u "+remote.Quote(p.InitUser)+" 2>/dev/null")
	if err != nil {
		return nil, err
	}
	// Nothing to purge if it is gone, or if it is root and would take the machine
	// with it.
	if uid == "" || uid == "0" {
		return nil, nil
	}

	return []Action{{
		Desc: "delete user " + p.InitUser,
		// userdel refuses while the account still has processes, and a killed one
		// lingers until it is reaped, so wait for the uid to clear.
		Run: func(r Runner) error {
			q := remote.Quote(p.InitUser)
			return root(r, "pkill -KILL -u "+q+" >/dev/null 2>&1; "+
				"for i in $(seq 100); do pgrep -u "+q+" >/dev/null || break; sleep 0.1; done; "+
				"userdel --remove "+q)
		},
	}}, nil
}
