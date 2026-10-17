package stage

// Services turns off what the image started but nothing here uses.
type Services struct {
	Disable []string
}

func (s *Services) Name() string { return "services" }

func (s *Services) Plan(r Runner) ([]Action, error) {
	var changes []change
	for _, svc := range s.Disable {
		changes = append(changes, change{
			desc: "disable " + svc,
			done: func(r Runner) (bool, error) {
				// Already off, or never installed on this image.
				return exitOK(r, "! systemctl is-enabled "+svc+" >/dev/null 2>&1 && ! systemctl is-active "+svc+" >/dev/null 2>&1")
			},
			do: func(r Runner) error { return root(r, "systemctl disable --now "+svc) },
		})
	}
	return plan(r, changes)
}
