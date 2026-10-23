// Copyright 2026 git-next contributors. MIT license.
package marshal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
)

// P4 -G uses Python's documented marshal wire format; no interpreter is loaded.
type Decoder struct {
	r        *bufio.Reader
	refs     []any
	interned []string
	budget   int64
	depth    int
}

func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: bufio.NewReader(r)} }
func (d *Decoder) Decode() (any, error) {
	d.refs = nil
	d.interned = nil
	d.budget = 128 << 20
	d.depth = 0
	return d.value()
}
func (d *Decoder) read(n int) ([]byte, error) {
	if n < 0 || int64(n) > d.budget {
		return nil, fmt.Errorf("marshal record exceeds bounds")
	}
	d.budget -= int64(n)
	b := make([]byte, n)
	_, e := io.ReadFull(d.r, b)
	return b, e
}
func (d *Decoder) i32() (int32, error) {
	b, e := d.read(4)
	if e != nil {
		return 0, e
	}
	return int32(binary.LittleEndian.Uint32(b)), nil
}
func (d *Decoder) size() (int, error) {
	n, e := d.i32()
	if e != nil {
		return 0, e
	}
	if n < 0 || n > 16<<20 {
		return 0, fmt.Errorf("invalid marshal size")
	}
	return int(n), nil
}

type nullValue struct{}

func (d *Decoder) value() (value any, err error) {
	if d.depth >= 200 {
		return nil, fmt.Errorf("marshal nesting limit exceeded")
	}
	d.depth++
	defer func() { d.depth-- }()
	b, e := d.read(1)
	if e != nil {
		return nil, e
	}
	tag := b[0] & 127
	reference := -1
	if b[0]&128 != 0 {
		reference = len(d.refs)
		d.refs = append(d.refs, nil)
		defer func() {
			if err == nil {
				d.refs[reference] = value
			}
		}()
	}
	switch tag {
	case '0':
		return nullValue{}, nil
	case 'N':
		return nil, nil
	case 'F':
		return false, nil
	case 'T':
		return true, nil
	case 'i':
		n, e := d.i32()
		return int64(n), e
	case 'I':
		b, e := d.read(8)
		if e != nil {
			return nil, e
		}
		return int64(binary.LittleEndian.Uint64(b)), nil
	case 'l':
		n, e := d.i32()
		if e != nil {
			return nil, e
		}
		negative := n < 0
		if negative {
			n = -n
		}
		if n > 1<<20 {
			return nil, fmt.Errorf("marshal integer exceeds bounds")
		}
		number := new(big.Int)
		digits, e := d.read(int(n) * 2)
		if e != nil {
			return nil, e
		}
		for i := int(n) - 1; i >= 0; i-- {
			digit := binary.LittleEndian.Uint16(digits[i*2:])
			if digit >= 1<<15 {
				return nil, fmt.Errorf("invalid marshal integer digit")
			}
			number.Lsh(number, 15)
			number.Add(number, new(big.Int).SetUint64(uint64(digit)))
		}
		if negative {
			number.Neg(number)
		}
		if number.IsInt64() {
			return number.Int64(), nil
		}
		return number, nil
	case 's', 't', 'u', 'a', 'A':
		n, e := d.size()
		if e != nil {
			return nil, e
		}
		b, e := d.read(n)
		if e != nil {
			return nil, e
		}
		if tag == 't' {
			d.interned = append(d.interned, string(b))
		}
		if tag == 's' {
			return b, nil
		}
		return string(b), nil
	case 'z', 'Z':
		b, e := d.read(1)
		if e != nil {
			return nil, e
		}
		text, e := d.read(int(b[0]))
		return string(text), e
	case 'R':
		n, e := d.i32()
		if e != nil || n < 0 || int(n) >= len(d.interned) {
			return nil, fmt.Errorf("invalid marshal interned-string reference")
		}
		return d.interned[n], nil
	case 'r':
		n, e := d.i32()
		if e != nil || n < 0 || int(n) >= len(d.refs) || d.refs[n] == nil {
			return nil, fmt.Errorf("invalid marshal object reference")
		}
		return d.refs[n], nil
	case 'g':
		b, e := d.read(8)
		if e != nil {
			return nil, e
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(b)), nil
	case 'f':
		b, e := d.read(1)
		if e != nil {
			return nil, e
		}
		b, e = d.read(int(b[0]))
		if e != nil {
			return nil, e
		}
		return strconv.ParseFloat(string(b), 64)
	case '{':
		m := map[string]any{}
		if reference >= 0 {
			d.refs[reference] = m
		}
		for {
			key, e := d.value()
			if e != nil {
				return nil, e
			}
			if _, ok := key.(nullValue); ok {
				return m, nil
			}
			var name string
			switch k := key.(type) {
			case string:
				name = k
			case []byte:
				name = string(k)
			default:
				return nil, fmt.Errorf("marshal dictionary key is not a byte/text string")
			}
			if _, exists := m[name]; exists {
				return nil, fmt.Errorf("duplicate marshal dictionary key")
			}
			v, e := d.value()
			if e != nil {
				return nil, e
			}
			m[name] = v
			if len(m) > 1<<20 {
				return nil, fmt.Errorf("marshal dictionary exceeds bounds")
			}
		}
	case '(', '[', '<', '>', ')':
		n := 0
		if tag == ')' {
			b, e := d.read(1)
			if e != nil {
				return nil, e
			}
			n = int(b[0])
		} else {
			n, e = d.size()
			if e != nil {
				return nil, e
			}
		}
		values := make([]any, n)
		if reference >= 0 {
			d.refs[reference] = values
		}
		for i := range values {
			values[i], e = d.value()
			if e != nil {
				return nil, e
			}
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported marshal tag 0x%x", tag)
	}
}
func Encode(w io.Writer, v any) error { return encode(w, v, 0) }
func put(w io.Writer, b []byte) error { _, e := w.Write(b); return e }
func writeSize(w io.Writer, n int) error {
	if n < 0 || n > 128<<20 {
		return fmt.Errorf("marshal value exceeds bounds")
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(n))
	return put(w, b[:])
}
func encode(w io.Writer, v any, depth int) error {
	if depth >= 200 {
		return fmt.Errorf("marshal nesting limit exceeded")
	}
	switch v := v.(type) {
	case nil:
		return put(w, []byte{'N'})
	case bool:
		if v {
			return put(w, []byte{'T'})
		}
		return put(w, []byte{'F'})
	case string:
		return encode(w, []byte(v), depth)
	case []byte:
		if e := put(w, []byte{'s'}); e != nil {
			return e
		}
		if e := writeSize(w, len(v)); e != nil {
			return e
		}
		return put(w, v)
	case int:
		return encode(w, int64(v), depth)
	case int32:
		return encode(w, int64(v), depth)
	case int64:
		if v >= math.MinInt32 && v <= math.MaxInt32 {
			if e := put(w, []byte{'i'}); e != nil {
				return e
			}
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], uint32(v))
			return put(w, b[:])
		}
		if e := put(w, []byte{'I'}); e != nil {
			return e
		}
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(v))
		return put(w, b[:])
	case float64:
		if e := put(w, []byte{'g'}); e != nil {
			return e
		}
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		return put(w, b[:])
	case map[string]any:
		if e := put(w, []byte{'{'}); e != nil {
			return e
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if e := encode(w, k, depth+1); e != nil {
				return e
			}
			if e := encode(w, v[k], depth+1); e != nil {
				return e
			}
		}
		return put(w, []byte{'0'})
	case []any:
		if e := put(w, []byte{'['}); e != nil {
			return e
		}
		if e := writeSize(w, len(v)); e != nil {
			return e
		}
		for _, item := range v {
			if e := encode(w, item, depth+1); e != nil {
				return e
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported marshal type %T", v)
	}
}
