package sysenc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/xiaofsec/ebpf/internal"
	qt "github.com/frankban/quicktest"
	"github.com/xiaofsec/go-cmp/cmp/cmpopts"
)

type testcase struct {
	new        func() any
	zeroAllocs bool
}

type struc struct {
	A uint64
	B uint32
}

type explicitPad struct {
	_ uint32
}

func testcases() []testcase {
	return []testcase{
		{func() any { return new([1]uint64) }, true},
		{func() any { return new(int16) }, true},
		{func() any { return new(uint16) }, true},
		{func() any { return new(int32) }, true},
		{func() any { return new(uint32) }, true},
		{func() any { return new(int64) }, true},
		{func() any { return new(uint64) }, true},
		{func() any { return make([]byte, 9) }, true},
		{func() any { return new(explicitPad) }, true},
		{func() any { return make([]explicitPad, 0) }, false},
		{func() any { return make([]explicitPad, 1) }, false},
		{func() any { return make([]explicitPad, 2) }, false},
		{func() any { return new(struc) }, false},
		{func() any { return make([]struc, 0) }, false},
		{func() any { return make([]struc, 1) }, false},
		{func() any { return make([]struc, 2) }, false},
		{func() any { return int16(math.MaxInt16) }, false},
		{func() any { return uint16(math.MaxUint16) }, false},
		{func() any { return int32(math.MaxInt32) }, false},
		{func() any { return uint32(math.MaxUint32) }, false},
		{func() any { return int64(math.MaxInt64) }, false},
		{func() any { return uint64(math.MaxUint64) }, false},
		{func() any { return struc{math.MaxUint64, math.MaxUint32} }, false},
	}
}

func TestMarshal(t *testing.T) {
	for _, test := range testcases() {
		value := test.new()
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			var want bytes.Buffer
			if err := binary.Write(&want, internal.NativeEndian, value); err != nil {
				t.Fatal(err)
			}

			have := make([]byte, want.Len())
			buf, err := Marshal(value, binary.Size(value))
			if err != nil {
				t.Fatal(err)
			}
			qt.Assert(t, buf.CopyTo(have), qt.Equals, want.Len())
			qt.Assert(t, have, qt.CmpEquals(cmpopts.EquateEmpty()), want.Bytes())
		})
	}
}

func TestMarshalAllocations(t *testing.T) {
	allocationsPerMarshal := func(t *testing.T, data any) float64 {
		size := binary.Size(data)
		return testing.AllocsPerRun(5, func() {
			_, err := Marshal(data, size)
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, test := range testcases() {
		if !test.zeroAllocs {
			continue
		}

		value := test.new()
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			qt.Assert(t, allocationsPerMarshal(t, value), qt.Equals, float64(0))
		})
	}
}

func TestUnmarshal(t *testing.T) {
	for _, test := range testcases() {
		value := test.new()
		if !canUnmarshalInto(value) {
			continue
		}

		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			want := test.new()
			buf := randomiseValue(t, want)

			qt.Assert(t, Unmarshal(value, buf), qt.IsNil)
			qt.Assert(t, value, qt.DeepEquals, want)
		})
	}
}

func TestUnmarshalAllocations(t *testing.T) {
	allocationsPerUnmarshal := func(t *testing.T, data any, buf []byte) float64 {
		return testing.AllocsPerRun(5, func() {
			err := Unmarshal(data, buf)
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, test := range testcases() {
		if !test.zeroAllocs {
			continue
		}

		value := test.new()
		if !canUnmarshalInto(value) {
			continue
		}

		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			buf := make([]byte, binary.Size(value))
			qt.Assert(t, allocationsPerUnmarshal(t, value, buf), qt.Equals, float64(0))
		})
	}
}

func TestUnsafeBackingMemory(t *testing.T) {
	marshalNative := func(t *testing.T, data any) []byte {
		t.Helper()

		var buf bytes.Buffer
		qt.Assert(t, binary.Write(&buf, internal.NativeEndian, data), qt.IsNil)
		return buf.Bytes()
	}

	for _, test := range []struct {
		name  string
		value any
	}{
		{
			"slice",
			[]uint32{1, 2},
		},
		{
			"pointer to slice",
			&[]uint32{2},
		},
		{
			"pointer to array",
			&[2]uint64{},
		},
		{
			"pointer to int64",
			new(int64),
		},
		{
			"pointer to struct",
			&struct {
				A, B uint16
				C    uint32
			}{},
		},
		{
			"struct with explicit padding",
			&struct{ _ uint64 }{},
		},
	} {
		t.Run("valid: "+test.name, func(t *testing.T) {
			want := marshalNative(t, test.value)
			have := unsafeBackingMemory(test.value)
			qt.Assert(t, have, qt.DeepEquals, want)
		})
	}

	for _, test := range []struct {
		name  string
		value any
	}{
		{
			"nil",
			nil,
		},
		{
			"nil slice",
			([]byte)(nil),
		},
		{
			"nil pointer",
			(*uint64)(nil),
		},
		{
			"nil pointer to slice",
			(*[]uint32)(nil),
		},
		{
			"nil pointer to array",
			(*[2]uint64)(nil),
		},
		{
			"unexported field",
			&struct{ a uint64 }{},
		},
		{
			"struct containing pointer",
			&struct{ A *uint64 }{},
		},
		{
			"struct with trailing padding",
			&struc{},
		},
		{
			"struct with interspersed padding",
			&struct {
				B uint32
				A uint64
			}{},
		},
		{
			"padding between slice entries",
			&[]struc{{}},
		},
		{
			"padding between array entries",
			&[2]struc{},
		},
	} {
		t.Run("invalid: "+test.name, func(t *testing.T) {
			qt.Assert(t, unsafeBackingMemory(test.value), qt.IsNil)
		})
	}
}

func BenchmarkMarshal(b *testing.B) {
	for _, test := range testcases() {
		value := test.new()
		b.Run(fmt.Sprintf("%T", value), func(b *testing.B) {
			size := binary.Size(value)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = Marshal(value, size)
			}
		})
	}
}

func BenchmarkUnmarshal(b *testing.B) {
	for _, test := range testcases() {
		value := test.new()
		if !canUnmarshalInto(value) {
			continue
		}

		b.Run(fmt.Sprintf("%T", value), func(b *testing.B) {
			size := binary.Size(value)
			buf := make([]byte, size)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = Unmarshal(value, buf)
			}
		})
	}
}

func randomiseValue(tb testing.TB, value any) []byte {
	tb.Helper()

	size := binary.Size(value)
	if size == -1 {
		tb.Fatalf("Can't unmarshal into %T", value)
	}

	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i)
	}

	err := binary.Read(bytes.NewReader(buf), internal.NativeEndian, value)
	qt.Assert(tb, err, qt.IsNil)

	return buf
}

func canUnmarshalInto(data any) bool {
	kind := reflect.TypeOf(data).Kind()
	return kind == reflect.Slice || kind == reflect.Pointer
}
