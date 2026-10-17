package stage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Kubeconfig copies the control plane's kubeconfig to ~/.kube/<name>.yaml, renamed
// after the node and pointed at its tailnet address, which is what still works once
// the public ports are closed.
type Kubeconfig struct {
	Node string // the k3s node name, which the file and context are named after
}

func (k *Kubeconfig) Name() string { return "kubeconfig" }

func (k *Kubeconfig) Plan(r Runner) ([]Action, error) {
	raw, err := output(r, "cat /etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, err
	}
	// k3s has not written one yet, which is what a plan against a fresh node sees.
	if raw == "" {
		return nil, nil
	}

	address, err := tailnetAddress(r)
	if err != nil {
		return nil, err
	}
	content, err := rewrite(raw, "https://"+address+":6443", k.Node)
	if err != nil {
		return nil, err
	}

	path, err := localPath(".kube", k.Node+".yaml")
	if err != nil {
		return nil, err
	}
	return plan(r, []change{{
		desc: "write " + path,
		done: func(r Runner) (bool, error) { return localFileIs(path, content) },
		do:   func(r Runner) error { return writeLocal(path, content) },
	}})
}

// rewrite renames the single cluster, context and user k3s writes, and points the
// server at address.
func rewrite(raw, address, name string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}

	for _, cluster := range entries(doc["clusters"]) {
		if nested, ok := cluster["cluster"].(map[string]any); ok {
			nested["server"] = address
		}
		cluster["name"] = name
	}
	for _, context := range entries(doc["contexts"]) {
		if nested, ok := context["context"].(map[string]any); ok {
			nested["cluster"] = name
			nested["user"] = name
		}
		context["name"] = name
	}
	for _, user := range entries(doc["users"]) {
		user["name"] = name
	}
	doc["current-context"] = name

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return "", err
	}
	return out.String(), encoder.Close()
}

func entries(list any) []map[string]any {
	items, _ := list.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if entry, ok := item.(map[string]any); ok {
			out = append(out, entry)
		}
	}
	return out
}

func localPath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

func writeLocal(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func localFileIs(path, content string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(b) == content, nil
}
