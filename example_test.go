package main

import (
	"testing"

	"github.com/rboyer/devconsul/config"
	"github.com/stretchr/testify/require"
)

func TestExamples(t *testing.T) {
	_, err := config.LoadConfig("example-config.hcl")
	require.NoError(t, err)
}
