package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"

	"github.com/thec1oud/dql/core/annotate"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeAnnotationsInvalid: an annotations entry that is not a mapping of
// annotation keys to values (§1.3).
const CodeAnnotationsInvalid = "config-annotations-invalid"

// ConfigAnnotations returns the annotations: entries as IR annotations,
// keyed by the qualified name they are written under, as
// catalog.SourceOutput.Config takes them (§1.3, §7.1). Keys are core.<key>
// unless written x.<plugin>.<key> (§7.2), sorted. Each origin is
// ORIGIN_CONFIG at <file>#annotations.<qualified name> (§7.6), where file
// is the name of the file Load read; the plugin that serves the key's
// catalog is the caller's to set, since only running the sources tells
// which catalog each one produced.
//
// A value takes the form of its key (§7.2, §7.4). A key written without a
// value, { nonnull }, has none. A list key, such as logical-key, takes a
// sequence, or one name for a list of one. A words key, such as name, takes
// a sequence of words, or a name that stays a string, because only the core
// splits words (§4.2). Any other value is a string, a list or a struct as
// YAML writes it: a scalar keeps its text as written, so 1.10 stays "1.10".
// A value of the wrong form is the core's to report (§7.3).
func (c *Config) ConfigAnnotations() (map[string][]*irv1.Annotation, []*irv1.Diagnostic) {
	out := map[string][]*irv1.Annotation{}
	var ds []*irv1.Diagnostic
	var targets []string
	for k := range c.Annotations {
		targets = append(targets, k)
	}
	slices.Sort(targets)
	for _, target := range targets {
		n := c.Annotations[target]
		if n.Kind != yaml.MappingNode {
			ds = append(ds, &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_ERROR, Code: CodeAnnotationsInvalid, Message: fmt.Sprintf(
				"%s line %d: annotations.%s is not a mapping; write the annotations of an object as { key: value, ... }, such as { name: user } (§1.3)",
				c.name(), n.Line, target)})
			continue
		}
		origin := &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_CONFIG, Location: c.name() + "#annotations." + target}
		var anns []*irv1.Annotation
		for i := 0; i < len(n.Content); i += 2 {
			key := annotate.IRKey(n.Content[i].Value)
			anns = append(anns, &irv1.Annotation{Key: key, Value: value(key, n.Content[i+1]), Origin: origin})
		}
		slices.SortStableFunc(anns, func(a, b *irv1.Annotation) int { return strings.Compare(a.Key, b.Key) })
		out[target] = anns
	}
	return out, ds
}

func (c *Config) name() string { return cmp.Or(c.file, "dql.yaml") }

// value types a YAML value by the form of key.
func value(key string, n *yaml.Node) *irv1.AnnotationValue {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	k, _ := annotate.Lookup(key)
	switch {
	case isNull(n):
		return nil
	case k.Form == annotate.FormList && n.Kind == yaml.ScalarNode:
		return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: &structpb.ListValue{
			Values: []*structpb.Value{structpb.NewStringValue(n.Value)}}}}
	case k.Form == annotate.FormWords && n.Kind == yaml.SequenceNode:
		return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Words{Words: element(n).GetListValue()}}
	}
	switch v := element(n); {
	case n.Kind == yaml.SequenceNode:
		return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_List{List: v.GetListValue()}}
	case n.Kind == yaml.MappingNode:
		return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_Struct{Struct: v.GetStructValue()}}
	default:
		return &irv1.AnnotationValue{Kind: &irv1.AnnotationValue_String_{String_: n.Value}}
	}
}

// element converts a YAML value inside a list or struct: a null, a scalar's
// text, a list or a struct.
func element(n *yaml.Node) *structpb.Value {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	switch {
	case isNull(n):
		return structpb.NewNullValue()
	case n.Kind == yaml.SequenceNode:
		l := &structpb.ListValue{}
		for _, e := range n.Content {
			l.Values = append(l.Values, element(e))
		}
		return structpb.NewListValue(l)
	case n.Kind == yaml.MappingNode:
		s := &structpb.Struct{Fields: map[string]*structpb.Value{}}
		for i := 0; i < len(n.Content); i += 2 {
			s.Fields[n.Content[i].Value] = element(n.Content[i+1])
		}
		return structpb.NewStructValue(s)
	default:
		return structpb.NewStringValue(n.Value)
	}
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" }
