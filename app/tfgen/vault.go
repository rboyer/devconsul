package tfgen

func VaultConfig() *FileResource {
	return File("cache/vault-config.hcl",
		Embed("templates/vault-config.hcl"))
}

func VaultContainer() Resource {
	return Embed("templates/container-vault.tf")
}
