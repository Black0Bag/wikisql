package kb

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
)

var ErrNotFound = errors.New("not found")

// ValidationError 表示请求参数不符合要求，HTTP 层应映射为 400。
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func NewValidationError(msg string) error {
	return &ValidationError{msg: msg}
}

func float32bits(f float32) uint32 {
	return math.Float32bits(f)
}

func float32frombits(b uint32) float32 {
	return math.Float32frombits(b)
}
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
