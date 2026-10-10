package fixture

import (
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: batch
value: 1000
---
apiVersion: v1
kind: Pod
metadata:
  name: api-1
  namespace: team-a
spec:
  priorityClassName: ghost
  nodeName: node-1
  containers:
    - name: app
      image: nginx
---
apiVersion: v1
kind: Node
metadata:
  name: node-1
`

func TestDecode(t *testing.T) {
	in, err := Decode([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Pods) != 1 || in.Pods[0].Name != "api-1" || in.Pods[0].Namespace != "team-a" {
		t.Fatalf("pods = %+v", in.Pods)
	}
	if len(in.PriorityClasses) != 1 || in.PriorityClasses[0].Value != 1000 {
		t.Fatalf("classes = %+v", in.PriorityClasses)
	}
	if len(in.Nodes) != 1 || in.Nodes[0].Name != "node-1" {
		t.Fatalf("nodes = %+v", in.Nodes)
	}
}

func TestDecodeList(t *testing.T) {
	in, err := Decode([]byte(`
apiVersion: v1
kind: PodList
items:
  - metadata: {name: p1, namespace: a}
  - metadata: {name: p2, namespace: a}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Pods) != 2 {
		t.Fatalf("pods = %+v", in.Pods)
	}
}

func TestDecodeRejectsUnknownKind(t *testing.T) {
	_, err := Decode([]byte("apiVersion: v1\nkind: Service\nmetadata: {name: s}\n"))
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("err = %v, want unsupported kind", err)
	}
}

func TestDecodeRejectsMissingKind(t *testing.T) {
	if _, err := Decode([]byte("metadata: {name: x}\n")); err == nil {
		t.Fatal("expected an error for a document with no kind")
	}
}

func TestDecodeRejectsTypo(t *testing.T) {
	_, err := Decode([]byte("kind: Pod\nmetadata: {name: p}\nspec: {priorityClassNam: oops}\n"))
	if err == nil {
		t.Fatal("expected a strict-decode error for a misspelled field")
	}
}

func TestLoadExampleFixtures(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no example fixtures found")
	}
	for _, path := range matches {
		in, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(in.Pods) == 0 {
			t.Fatalf("%s: fixture has no pods", path)
		}
	}
}
