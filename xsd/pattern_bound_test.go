package xsd

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A pattern facet had no bound at all: 16 types holding sequential \p{L}{1000}
// repeats in a 354 KB schema retained 1.4 GB.
func TestPatternProgramSizeIsBounded(t *testing.T) {
	_, err := compilePattern(strings.Repeat(`\p{L}{1000}`, 150))
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("want a program-size refusal wrapping ErrResourceLimit, got %v", err)
	}
	if _, err := compilePattern(strings.Repeat(`\p{L}{1000}`, 50)); err != nil {
		t.Fatalf("a pattern under the bound must compile: %v", err)
	}
}
