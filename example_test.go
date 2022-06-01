package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rboyer/devconsul/config"
)

func TestExamples(t *testing.T) {
	_, err := config.LoadConfig("example-config.hcl")
	require.NoError(t, err)
}
