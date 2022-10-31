package tfgen

import "fmt"

func DockerNetwork(name, cidr string) Resource {
	return Text(fmt.Sprintf(`
resource "docker_network" %[1]q {
  name       = %[1]q
  attachable = true
  ipam_config {
    subnet = %[2]q
  }
  labels {
    label = "devconsul"
    value = "1"
  }
}`, name, cidr))
}

func DockerVolume(name string) Resource {
	return Text(fmt.Sprintf(`
resource "docker_volume" %[1]q {
  name       = %[1]q
  labels {
    label = "devconsul"
    value = "1"
  }
}`, name))
}

func DockerImage(name, image string) Resource {
	return Text(fmt.Sprintf(`
resource "docker_image" %[1]q {
  name = %[2]q
  keep_locally = true
}`, name, image))
}
