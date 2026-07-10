package mockhw

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRegisterFileReadWrite exercises registerFile.read/write directly,
// covering every switch branch dispatch's opcode tests don't reach on their
// own: the OUT1/IN1 mirror pair, the accepted-but-inert ENABLE/ENABLE1
// W1TS/W1TC writes, the generic masked read-modify-write default branch,
// and a read of an address nothing has ever written (implicit zero value).
func TestRegisterFileReadWrite(t *testing.T) {
	p := profileESP32S2
	r := newRegisterFile()

	t.Run("magic register", func(t *testing.T) {
		assert.Equal(t, p.magicValue, r.read(p, p.magicRegAddr))
	})

	t.Run("OUT/IN mirror", func(t *testing.T) {
		r.write(p, p.outW1TS, 1<<3, 0xFFFFFFFF)
		assert.Equal(t, uint32(1<<3), r.read(p, p.inAddr))

		r.write(p, p.outW1TC, 1<<3, 0xFFFFFFFF)
		assert.Equal(t, uint32(0), r.read(p, p.inAddr))
	})

	t.Run("OUT1/IN1 mirror", func(t *testing.T) {
		r.write(p, p.out1W1TS, 1<<5, 0xFFFFFFFF)
		assert.Equal(t, uint32(1<<5), r.read(p, p.in1Addr))

		r.write(p, p.out1W1TC, 1<<5, 0xFFFFFFFF)
		assert.Equal(t, uint32(0), r.read(p, p.in1Addr))
	})

	t.Run("ENABLE/ENABLE1 writes accepted with no loopback effect", func(t *testing.T) {
		r.write(p, p.enableW1TS, 0xFFFFFFFF, 0xFFFFFFFF)
		r.write(p, p.enableW1TC, 0xFFFFFFFF, 0xFFFFFFFF)
		r.write(p, p.enable1W1TS, 0xFFFFFFFF, 0xFFFFFFFF)
		r.write(p, p.enable1W1TC, 0xFFFFFFFF, 0xFFFFFFFF)

		assert.Equal(t, uint32(0), r.read(p, p.inAddr), "IN must be unaffected by ENABLE writes")
		assert.Equal(t, uint32(0), r.read(p, p.in1Addr), "IN1 must be unaffected by ENABLE writes")
	})

	t.Run("generic masked RMW default branch", func(t *testing.T) {
		const genericAddr = 0x3F400100

		r.write(p, genericAddr, 0xFF, 0x0F)
		assert.Equal(t, uint32(0x0F), r.read(p, genericAddr), "only masked bits apply")

		r.write(p, genericAddr, 0xF0, 0xF0)
		assert.Equal(t, uint32(0xFF), r.read(p, genericAddr), "second RMW OR's in the newly masked bits")
	})

	t.Run("unwritten address reads zero", func(t *testing.T) {
		assert.Equal(t, uint32(0), r.read(p, 0xDEADBEEF))
	})
}
