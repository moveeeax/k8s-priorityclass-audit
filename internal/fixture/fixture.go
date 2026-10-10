// Package fixture loads cluster state from a multi-document YAML file so the
// audit can run (and be demonstrated) with no cluster at all.
package fixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/moveeeax/k8s-priorityclass-audit/internal/audit"
)

// Load reads Pod, PriorityClass, Node and List documents from path.
func Load(path string) (audit.Input, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return audit.Input{}, err
	}
	return Decode(data)
}

// Decode parses the YAML bytes. Unknown kinds are an error, not a silent skip:
// a typo in a fixture otherwise looks like a clean cluster.
func Decode(data []byte) (audit.Input, error) {
	var in audit.Input
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for i := 0; ; i++ {
		var raw map[string]interface{}
		err := dec.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return in, fmt.Errorf("document %d: %w", i, err)
		}
		if len(raw) == 0 {
			continue
		}
		if err := appendObject(&in, raw); err != nil {
			return in, fmt.Errorf("document %d: %w", i, err)
		}
	}
	return in, nil
}

func appendObject(in *audit.Input, raw map[string]interface{}) error {
	kind, _ := raw["kind"].(string)
	switch kind {
	case "Pod":
		var pod corev1.Pod
		if err := remarshal(raw, &pod); err != nil {
			return err
		}
		in.Pods = append(in.Pods, pod)
	case "PriorityClass":
		var pc schedulingv1.PriorityClass
		if err := remarshal(raw, &pc); err != nil {
			return err
		}
		in.PriorityClasses = append(in.PriorityClasses, pc)
	case "Node":
		var node corev1.Node
		if err := remarshal(raw, &node); err != nil {
			return err
		}
		in.Nodes = append(in.Nodes, node)
	case "List", "PodList", "PriorityClassList", "NodeList":
		items, _ := raw["items"].([]interface{})
		for _, item := range items {
			m, ok := item.(map[string]interface{})
			if !ok {
				return fmt.Errorf("list item is not an object")
			}
			if _, has := m["kind"]; !has {
				m["kind"] = listItemKind(kind)
			}
			if err := appendObject(in, m); err != nil {
				return err
			}
		}
	case "":
		return fmt.Errorf("document has no kind")
	default:
		return fmt.Errorf("unsupported kind %q (want Pod, PriorityClass, Node or a *List)", kind)
	}
	return nil
}

func listItemKind(listKind string) string {
	switch listKind {
	case "PodList":
		return "Pod"
	case "PriorityClassList":
		return "PriorityClass"
	case "NodeList":
		return "Node"
	default:
		return ""
	}
}

// remarshal decodes strictly: an unknown field in a fixture is a typo, and a
// typo that is silently dropped turns into a passing test for a rule that
// never ran.
func remarshal(raw map[string]interface{}, out interface{}) error {
	buf, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
