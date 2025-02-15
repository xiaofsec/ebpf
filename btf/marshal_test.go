package btf

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/xiaofsec/ebpf/internal"
	"github.com/xiaofsec/ebpf/internal/testutils"
	"github.com/xiaofsec/go-cmp/cmp"

	qt "github.com/frankban/quicktest"
)

func TestBuilderMarshal(t *testing.T) {
	typ := &Int{
		Name:     "foo",
		Size:     2,
		Encoding: Signed | Char,
	}

	want := []Type{
		(*Void)(nil),
		typ,
		&Pointer{typ},
		&Typedef{"baz", typ},
	}

	b, err := NewBuilder(want)
	qt.Assert(t, err, qt.IsNil)

	cpy := *b
	buf, err := b.Marshal(nil, &MarshalOptions{Order: internal.NativeEndian})
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, b, qt.CmpEquals(cmp.AllowUnexported(*b)), &cpy, qt.Commentf("Marshaling should not change Builder state"))

	have, err := loadRawSpec(bytes.NewReader(buf), internal.NativeEndian, nil)
	qt.Assert(t, err, qt.IsNil, qt.Commentf("Couldn't parse BTF"))
	qt.Assert(t, have.types, qt.DeepEquals, want)
}

func TestBuilderAdd(t *testing.T) {
	i := &Int{
		Name:     "foo",
		Size:     2,
		Encoding: Signed | Char,
	}
	pi := &Pointer{i}

	var b Builder
	id, err := b.Add(pi)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, id, qt.Equals, TypeID(1), qt.Commentf("First non-void type doesn't get id 1"))

	id, err = b.Add(pi)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, id, qt.Equals, TypeID(1))

	id, err = b.Add(i)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, id, qt.Equals, TypeID(2), qt.Commentf("Second type doesn't get id 2"))

	id, err = b.Add(i)
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, id, qt.Equals, TypeID(2), qt.Commentf("Adding a type twice returns different ids"))

	id, err = b.Add(&Typedef{"baz", i})
	qt.Assert(t, err, qt.IsNil)
	qt.Assert(t, id, qt.Equals, TypeID(3))
}

func TestRoundtripVMlinux(t *testing.T) {
	types := vmlinuxSpec(t).types

	// Randomize the order to force different permutations of walking the type
	// graph. Keep Void at index 0.
	testutils.Rand(t).Shuffle(len(types[1:]), func(i, j int) {
		types[i+1], types[j+1] = types[j+1], types[i+1]
	})

	seen := make(map[Type]bool)
limitTypes:
	for i, typ := range types {
		iter := postorderTraversal(typ, func(t Type) (skip bool) {
			return seen[t]
		})
		for iter.Next() {
			seen[iter.Type] = true
		}
		if len(seen) >= math.MaxInt16 {
			// IDs exceeding math.MaxUint16 can trigger a bug when loading BTF.
			// This can be removed once the patch lands.
			// See https://lore.kernel.org/bpf/20220909092107.3035-1-oss@lmb.io/
			types = types[:i]
			break limitTypes
		}
	}

	b, err := NewBuilder(types)
	qt.Assert(t, err, qt.IsNil)
	buf, err := b.Marshal(nil, KernelMarshalOptions())
	qt.Assert(t, err, qt.IsNil)

	rebuilt, err := loadRawSpec(bytes.NewReader(buf), binary.LittleEndian, nil)
	qt.Assert(t, err, qt.IsNil, qt.Commentf("round tripping BTF failed"))

	if n := len(rebuilt.types); n > math.MaxUint16 {
		t.Logf("Rebuilt BTF contains %d types which exceeds uint16, test may fail on older kernels", n)
	}

	h, err := NewHandleFromRawBTF(buf)
	testutils.SkipIfNotSupported(t, err)
	qt.Assert(t, err, qt.IsNil, qt.Commentf("loading rebuilt BTF failed"))
	h.Close()
}

func TestMarshalEnum64(t *testing.T) {
	enum := &Enum{
		Name:   "enum64",
		Size:   8,
		Signed: true,
		Values: []EnumValue{
			{"A", 0},
			{"B", 1},
		},
	}

	b, err := NewBuilder([]Type{enum})
	qt.Assert(t, err, qt.IsNil)
	buf, err := b.Marshal(nil, &MarshalOptions{
		Order:         internal.NativeEndian,
		ReplaceEnum64: true,
	})
	qt.Assert(t, err, qt.IsNil)

	spec, err := loadRawSpec(bytes.NewReader(buf), internal.NativeEndian, nil)
	qt.Assert(t, err, qt.IsNil)

	var have *Union
	err = spec.TypeByName("enum64", &have)
	qt.Assert(t, err, qt.IsNil)

	placeholder := &Int{Name: "enum64_placeholder", Size: 8, Encoding: Signed}
	qt.Assert(t, have, qt.DeepEquals, &Union{
		Name: "enum64",
		Size: 8,
		Members: []Member{
			{Name: "A", Type: placeholder},
			{Name: "B", Type: placeholder},
		},
	})
}

func BenchmarkMarshaler(b *testing.B) {
	spec := vmlinuxTestdataSpec(b)
	types := spec.types[:100]

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var b Builder
		for _, typ := range types {
			_, _ = b.Add(typ)
		}
		_, _ = b.Marshal(nil, nil)
	}
}

func BenchmarkBuildVmlinux(b *testing.B) {
	types := vmlinuxTestdataSpec(b).types

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var b Builder
		for _, typ := range types {
			_, _ = b.Add(typ)
		}
		_, _ = b.Marshal(nil, nil)
	}
}

func marshalNativeEndian(tb testing.TB, types []Type) []byte {
	tb.Helper()

	b, err := NewBuilder(types)
	qt.Assert(tb, err, qt.IsNil)
	buf, err := b.Marshal(nil, nil)
	qt.Assert(tb, err, qt.IsNil)
	return buf
}

func specFromTypes(tb testing.TB, types []Type) *Spec {
	tb.Helper()

	btf := marshalNativeEndian(tb, types)
	spec, err := loadRawSpec(bytes.NewReader(btf), internal.NativeEndian, nil)
	qt.Assert(tb, err, qt.IsNil)

	return spec
}
