package main

import (
	"github.com/rboyer/devconsul/infra"
)

type NetworkShape = infra.NetworkShape

const (
	NetworkShapeIslands = infra.NetworkShapeIslands
	NetworkShapeDual    = infra.NetworkShapeDual
	NetworkShapeFlat    = infra.NetworkShapeFlat
)
