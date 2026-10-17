package stage

import "fmt"

const registryConfig = "/etc/rancher/k3s/registries.yaml"

// Registry writes the image registry credentials k3s reads at startup, so nodes can
// pull private images.
type Registry struct {
	Role     string
	Username string
	Token    string
}

func (g *Registry) Name() string { return "registry" }

func (g *Registry) Plan(r Runner) ([]Action, error) {
	content := fmt.Sprintf(`configs:
  "ghcr.io":
    auth:
      username: %q
      password: %q
`, g.Username, g.Token)

	write := change{
		desc: "write " + registryConfig,
		done: func(r Runner) (bool, error) { return fileHas(r, registryConfig, content) },
		do: func(r Runner) error {
			if err := r.WriteRootFile(registryConfig, []byte(content), "0600", ""); err != nil {
				return err
			}
			return restartIfRunning(r, k3sUnit(g.Role))
		},
	}
	return plan(r, []change{write})
}
