package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeForGatus(t *testing.T) {
	input := "group/name_with,chars.#/+&"
	output := sanitizeForGatus(input)
	assert.Equal(t, "group-name-with-chars-----", output)
}
