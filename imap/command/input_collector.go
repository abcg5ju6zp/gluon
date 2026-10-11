package command

import (
	"github.com/ProtonMail/gluon/rfcparser"
)

type InputCollector struct {
	source     rfcparser.Reader
	bytes      []byte
	collecting bool
}

func NewInputCollector(source rfcparser.Reader) *InputCollector {
	return &InputCollector{
		source:     source,
		bytes:      make([]byte, 0, 128),
		collecting: true,
	}
}

func (i *InputCollector) Bytes() []byte {
	return i.bytes
}

// SetCollecting toggles collection of incoming bytes for logging. Collection is suspended
// while a large literal body is streamed so its bytes do not accumulate in memory.
func (i *InputCollector) SetCollecting(collecting bool) {
	i.collecting = collecting
}

func (i *InputCollector) Read(dst []byte) (int, error) {
	n, err := i.source.Read(dst)
	if err == nil && i.collecting {
		i.bytes = append(i.bytes, dst[0:n]...)
	}

	return n, err
}

func (i *InputCollector) ReadByte() (byte, error) {
	b, err := i.source.ReadByte()
	if err == nil && i.collecting {
		i.bytes = append(i.bytes, b)
	}

	return b, err
}

func (i *InputCollector) ReadBytes(delim byte) ([]byte, error) {
	b, err := i.source.ReadBytes(delim)
	if err == nil && i.collecting {
		i.bytes = append(i.bytes, b...)
	}

	return b, err
}

func (i *InputCollector) Reset() {
	i.bytes = i.bytes[:0]
}

func (i *InputCollector) SetSource(source rfcparser.Reader) {
	i.source = source
}
