package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvTruthy(t *testing.T) {
	cases := map[string]bool{
		"1":        true,
		"true":     true,
		"TRUE":     true,
		"True":     true,
		"t":        true,
		"":         false,
		"0":        false,
		"false":    false,
		"yes":      false,
		"nonsense": false,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := envTruthy(in)
			assert.Equal(t, want, got, "envTruthy(%q)", in)
		})
	}
}
