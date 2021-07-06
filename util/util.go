package util

import "fmt"

type Identifier struct {
	Name      string
	Namespace string
	Partition string
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

func (id *Identifier) String() string {
	return fmt.Sprintf("%s/%s/%s", id.Partition, id.Namespace, id.Name)
}

func (id *Identifier) ID() string {
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
