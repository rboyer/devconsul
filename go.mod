module github.com/rboyer/devconsul

go 1.13

replace github.com/hashicorp/consul/api => ../consul/api

require (
	github.com/armon/go-metrics v0.0.0-20190423201044-2801d9688273 // indirect
	github.com/google/btree v1.0.0 // indirect
	github.com/hashicorp/consul/api v1.7.0
	github.com/hashicorp/go-hclog v0.12.0
	github.com/hashicorp/go-msgpack v0.5.4 // indirect
	github.com/hashicorp/go-sockaddr v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.1
	github.com/hashicorp/golang-lru v0.5.1 // indirect
	github.com/hashicorp/hcl v1.0.1-0.20190430135223-99e2f22d1c94
	github.com/hashicorp/hcl/v2 v2.7.0
	github.com/rboyer/safeio v0.1.0
	github.com/stretchr/testify v1.4.0
)
