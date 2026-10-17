package config

import (
	"errors"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// FromFile reads a cluster file. Every node block is self-contained, credentials
// included; env() is how you avoid repeating them in each block.
func FromFile(path string) ([]Node, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, errors.New(diags.Error())
	}

	var parsed struct {
		Nodes []Node `hcl:"node,block"`
	}
	if diags := gohcl.DecodeBody(file.Body, evalContext(), &parsed); diags.HasErrors() {
		return nil, errors.New(diags.Error())
	}
	return parsed.Nodes, nil
}

// evalContext gives the file env(), so secrets stay in your shell and not in the file.
func evalContext() *hcl.EvalContext {
	return &hcl.EvalContext{
		Functions: map[string]function.Function{
			"env": function.New(&function.Spec{
				Params: []function.Parameter{{Name: "name", Type: cty.String}},
				Type:   function.StaticReturnType(cty.String),
				Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
					return cty.StringVal(os.Getenv(args[0].AsString())), nil
				},
			}),
		},
	}
}
