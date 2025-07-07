package unsafereflect_test

import (
	"testing"

	"github.com/skillian/unsafereflect"
	"github.com/skillian/unsafereflecttest"
)

func TestCycle(t *testing.T) {
	_ = unsafereflect.TypeOf(unsafereflecttest.A())
}
