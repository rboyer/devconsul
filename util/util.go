package util

import "fmt"

type Identifier struct {
	Name      string `json:",omitempty"`
	Namespace string `json:",omitempty"`
	Partition string `json:",omitempty"`
}

func NewIdentifier(name, namespace, partition string) Identifier {
	id := Identifier{
		Name:      name,
		Namespace: namespace,
		Partition: partition,
	}
	id.Normalize()
	return id
}

func (id *Identifier) Normalize() {
	id.Namespace = NamespaceOrDefault(id.Namespace)
	id.Partition = PartitionOrDefault(id.Partition)
}

func (id Identifier) String() string {
	return fmt.Sprintf("%s/%s/%s", id.Partition, id.Namespace, id.Name)
}

func (id Identifier) ID() string {
	return fmt.Sprintf("%s.%s.%s", id.Partition, id.Namespace, id.Name)
}

func PartitionOrDefault(name string) string {
	if name == "" {
		return "default"
	}
	return name
}
func NamespaceOrDefault(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

type Identifier2 struct {
	Name      string `json:",omitempty"`
	Partition string `json:",omitempty"`
}

func NewIdentifier2(name, partition string) Identifier2 {
	id := Identifier2{
		Name:      name,
		Partition: partition,
	}
	id.Normalize()
	return id
}

func (id *Identifier2) Normalize() {
	id.Partition = PartitionOrDefault(id.Partition)
}

func (id Identifier2) String() string {
	return fmt.Sprintf("%s/%s", id.Partition, id.Name)
}

func (id Identifier2) ID() string {
	return fmt.Sprintf("%s.%s", id.Partition, id.Name)
}
