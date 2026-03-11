package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateWorkloadNameRejectsDotNames(t *testing.T) {
	assert.Error(t, validateWorkloadName("."))
	assert.Error(t, validateWorkloadName(".."))
}
