package tailapi

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/tailscale/hujson"
)

// OwnerTag is what tailk3s's own node on the laptop wears. Tagged devices are exempt
// from key expiry, so it never has to be logged in again.
const OwnerTag = "tag:tailk3s"

// adminTagOwner is who may apply OwnerTag.
const adminTagOwner = "autogroup:admin"

// Access is what tailk3s needs the policy to say for the nodes it is provisioning.
type Access struct {
	SSHUser  string   // the account tailk3s logs into a node as
	NodeTags []string // the tags the nodes wear, one per node in the config
	JoinTags []string // control planes outside the config, which tailk3s reaches but does not own
}

// merge adds an owner entry for every tag the nodes wear, and the ssh rule that lets
// tailk3s reach them. Entries already in the policy are left as written, comments
// included.
func merge(src []byte, a Access) ([]byte, error) {
	root, err := hujson.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse the policy: %w", err)
	}
	policy, ok := root.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("the policy is not a JSON object")
	}

	// A node's key is asked for under that node's tag, so the tag has to have an owner
	// before it can be worn.
	owners, err := childObject(policy, "tagOwners")
	if err != nil {
		return nil, err
	}
	addTag(owners, OwnerTag, adminTagOwner)
	for _, tag := range a.NodeTags {
		addTag(owners, tag, OwnerTag)
	}

	rules, err := childArray(policy, "ssh")
	if err != nil {
		return nil, err
	}
	if err := allowSSH(rules, a); err != nil {
		return nil, err
	}

	return root.Pack(), nil
}

// addTag gives tag an owner, unless the policy already says who owns it.
func addTag(owners *hujson.Object, tag, owner string) {
	if find(owners, tag) != nil {
		return
	}
	appendMember(owners, tag, arrayValue(owner))
}

// allowSSH keeps a single rule letting tailk3s in, and keeps its destinations in step
// with the nodes in the config.
func allowSSH(rules *hujson.Array, a Access) error {
	for i := range rules.Elements {
		rule, ok := rules.Elements[i].Value.(*hujson.Object)
		if !ok || !holds(rule, "src", OwnerTag) || !holds(rule, "users", a.SSHUser) {
			continue
		}
		destinations, err := childArray(rule, "dst")
		if err != nil {
			return err
		}
		for _, tag := range slices.Concat(a.NodeTags, a.JoinTags) {
			if !contains(destinations, tag) {
				appendElement(destinations, *literalValue(tag))
			}
		}
		return nil
	}
	appendElement(rules, sshRule(a))
	return nil
}

// sshRule lets tailk3s's own node, and an admin, log in to the nodes. Later runs
// recognise it by its src and users.
func sshRule(a Access) hujson.Value {
	return hujson.Value{Value: &hujson.Object{
		Members: []hujson.ObjectMember{
			pair("action", literalValue("accept")),
			pair("src", arrayValue(OwnerTag, adminTagOwner)),
			pair("dst", arrayValue(slices.Concat(a.NodeTags, a.JoinTags)...)),
			pair("users", arrayValue(a.SSHUser)),
		},
		AfterExtra: []byte(" "),
	}}
}

// childObject returns the object under key, adding an empty one when it is absent.
func childObject(parent *hujson.Object, key string) (*hujson.Object, error) {
	found := find(parent, key)
	if found == nil {
		// Laid out the way a written policy is, so entries added to it line up.
		obj := &hujson.Object{AfterExtra: []byte("\n  ")}
		appendMember(parent, key, &hujson.Value{Value: obj})
		return obj, nil
	}
	obj, ok := found.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("the policy's %s is not an object", key)
	}
	return obj, nil
}

// childArray is childObject for an array.
func childArray(parent *hujson.Object, key string) (*hujson.Array, error) {
	found := find(parent, key)
	if found == nil {
		arr := &hujson.Array{AfterExtra: []byte("\n  ")}
		appendMember(parent, key, &hujson.Value{Value: arr})
		return arr, nil
	}
	arr, ok := found.Value.(*hujson.Array)
	if !ok {
		return nil, fmt.Errorf("the policy's %s is not an array", key)
	}
	return arr, nil
}

// find returns the value under key, or nil when the object has no such key.
func find(parent *hujson.Object, key string) *hujson.Value {
	want := string(hujson.String(key))
	for i := range parent.Members {
		if name, ok := parent.Members[i].Name.Value.(hujson.Literal); ok && string(name) == want {
			return &parent.Members[i].Value
		}
	}
	return nil
}

func holds(obj *hujson.Object, key, s string) bool {
	found := find(obj, key)
	if found == nil {
		return false
	}
	arr, ok := found.Value.(*hujson.Array)
	return ok && contains(arr, s)
}

func contains(arr *hujson.Array, s string) bool {
	want := string(hujson.String(s))
	for _, e := range arr.Elements {
		if lit, ok := e.Value.(hujson.Literal); ok && string(lit) == want {
			return true
		}
	}
	return false
}

// appendMember adds a member. The trivia before the closing brace moves in front of the
// member displacing it, so a comment never lands after a comma.
func appendMember(parent *hujson.Object, key string, value *hujson.Value) {
	before, after := ahead(parent.AfterExtra, true)
	if n := len(parent.Members); n > 0 {
		parent.Members[n-1].Value.AfterExtra = nil
	}
	value.BeforeExtra = []byte(" ")
	parent.Members = append(parent.Members, hujson.ObjectMember{
		Name:  hujson.Value{Value: hujson.String(key), BeforeExtra: before},
		Value: *value,
	})
	parent.AfterExtra = after
}

// appendElement is appendMember for an array.
func appendElement(arr *hujson.Array, elem hujson.Value) {
	before, after := ahead(arr.AfterExtra, false)
	if n := len(arr.Elements); n > 0 {
		arr.Elements[n-1].AfterExtra = nil
	}
	elem.BeforeExtra = before
	arr.Elements = append(arr.Elements, elem)
	arr.AfterExtra = after
}

// ahead splits the trivia before a closing brace: what goes in front of the entry
// displacing it, and what stays behind for the brace. A comment is kept, whitespace is
// redone to match the document.
func ahead(carried hujson.Extra, ownLine bool) (before, after hujson.Extra) {
	indent := []byte("\n  ")
	if i := bytes.LastIndexByte(carried, '\n'); i >= 0 {
		indent = bytes.Clone(carried[i:])
	} else if !ownLine && !hasComment(carried) {
		// A list already on one line stays on one line.
		return []byte(" "), nil
	}
	// Entries sit one level in from the brace that closes them.
	entry := append(bytes.Clone(indent), ' ', ' ')
	if !hasComment(carried) {
		return entry, indent
	}
	return append(bytes.TrimRight(carried, " \t\n\r"), entry...), indent
}

// hasComment reports whether extra carries a comment. Whitespace on its own is redone.
func hasComment(extra hujson.Extra) bool {
	return bytes.ContainsRune(extra, '/')
}

func pair(name string, value *hujson.Value) hujson.ObjectMember {
	value.BeforeExtra = []byte(" ")
	return hujson.ObjectMember{
		Name:  hujson.Value{Value: hujson.String(name), BeforeExtra: []byte(" ")},
		Value: *value,
	}
}

// arrayValue builds an array of strings. The space goes in front of an element, since an
// extra after the last one would read as a trailing comma.
func arrayValue(values ...string) *hujson.Value {
	arr := &hujson.Array{}
	for i, v := range values {
		e := hujson.Value{Value: hujson.String(v)}
		if i > 0 {
			e.BeforeExtra = []byte(" ")
		}
		arr.Elements = append(arr.Elements, e)
	}
	return &hujson.Value{Value: arr}
}

func literalValue(s string) *hujson.Value {
	return &hujson.Value{Value: hujson.String(s)}
}
