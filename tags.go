package retag

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// TODO(yar): Write implementation notes for TagMaker.

// A TagMaker interface is used by the Convert function to generate tags for structures.
// A type that implements TagMaker should be comparable.
type TagMaker interface {
	// MakeTag makes tag for the field the fieldIndex in the structureType.
	// Result should depends on constant parameters of creation of the TagMaker and parameters
	// passed to the MakeTag. The MakeTag should not produce side effects (like a pure function).
	MakeTag(structureType reflect.Type, fieldIndex int) reflect.StructTag
}

// Convert converts the given interface p, to a runtime-generated type.
// The type is generated on base of source type by the next rules:
//   - Analogous type with custom tags is generated for structures.
//     The tag for every field is generated by the maker;
//   - Type is replaced with a generated one if it has field, element or key of type
//     which should be replaced with its own analogue or if it is structure.
//	 - A type of private fields of structures is not modified.
//
// Convert panics if argument p has a type different from a pointer to structure.
// The maker's underlying type should be comparable. In different case panic occurs.
//
// Convert panics if the maker attempts to change a field tag of a structure with unexported fields
// because reflect package doesn't support creation of a structure type with private fields.
//
// Convert puts generated types in a cache by a key (source type + maker) to speed up
// handling of types. See notes in description of TagMaker interface to avoid
// the tricky situation with the cache.
//
// Convert doesn't support cyclic references because reflect package doesn't support generation of
// types with cyclic references. Passing cyclic structures to Convert will result in an infinite
// recursion.
//
// Convert doesn't support any interfaces, functions, chan and unsafe pointers.
// Interfaces is not supported because they requires memory-copy operations in most cases.
// Passing structures that contains unsupported types to Convert will result in a panic.
//
// BUG(yar): Convert panics on structure with a final zero-size field in go1.7.
// It is fixed in go1.8 (see github.com/golang/go/issues/18016).
func Convert(p interface{}, maker TagMaker) interface{} {
	strPtrVal := reflect.ValueOf(p)
	// TODO(yar): check type (pointer to the structure)
	newType := getType(strPtrVal.Type().Elem(), maker)
	newPtrVal := reflect.NewAt(newType, unsafe.Pointer(strPtrVal.Pointer()))
	return newPtrVal.Interface()
}

type cacheKey struct {
	reflect.Type
	TagMaker
}

var cache = struct {
	sync.RWMutex
	m map[cacheKey]reflect.Type
}{
	m: make(map[cacheKey]reflect.Type),
}

func getType(structType reflect.Type, maker TagMaker) reflect.Type {
	// TODO(yar): Improve syncronization for cases when one analogue
	// is produced concurently by different goroutines in the same time
	key := cacheKey{structType, maker}
	cache.RLock()
	t, ok := cache.m[key]
	cache.RUnlock()
	if !ok {
		t = makeType(structType, maker)
		cache.Lock()
		cache.m[key] = t
		cache.Unlock()
	}
	return t
}

// TODO(yar): Optimize cases when type is not modified.
func makeType(t reflect.Type, maker TagMaker) reflect.Type {
	switch t.Kind() {
	case reflect.Struct:
		return makeStructType(t, maker)
	case reflect.Ptr:
		return reflect.PtrTo(getType(t.Elem(), maker))
	case reflect.Array:
		return reflect.ArrayOf(t.Len(), getType(t.Elem(), maker))
	case reflect.Slice:
		return reflect.SliceOf(getType(t.Elem(), maker))
	case reflect.Map:
		return reflect.MapOf(getType(t.Key(), maker), getType(t.Elem(), maker))
	case
		reflect.Chan,
		reflect.Func,
		reflect.UnsafePointer,
		reflect.Interface:
		panic("tags.Map: Unsupported type: " + t.Kind().String())
	default:
		// don't modify type in another case
		return t
	}
}

func makeStructType(structType reflect.Type, maker TagMaker) reflect.Type {
	if structType.NumField() == 0 {
		return structType
	}
	changed := false
	hasPrivate := false
	fields := make([]reflect.StructField, 0, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		strField := structType.Field(i)
		if isExported(strField.Name) {
			oldType := strField.Type
			newType := getType(oldType, maker)
			strField.Type = newType
			if oldType != newType {
				changed = true
			}
			oldTag := strField.Tag
			newTag := maker.MakeTag(structType, i)
			strField.Tag = newTag
			if oldTag != newTag {
				changed = true
			}
		} else {
			hasPrivate = true
			if !structTypeConstructorBugWasFixed {
				// reflect.StructOf works with private fields and anonymous fields incorrect.
				// see issue https://github.com/golang/go/issues/17766
				strField.PkgPath = ""
				strField.Name = ""
			}
		}
		fields = append(fields, strField)
	}
	if !changed {
		return structType
	} else if hasPrivate {
		panic(fmt.Sprintf("unable to change tags for type %s, because it contains unexported fields", structType))
	}
	newType := reflect.StructOf(fields)
	compareStructTypes(structType, newType)
	return newType
}

func isExported(name string) bool {
	b := name[0]
	return !('a' <= b && b <= 'z') && b != '_'
}

func compareStructTypes(source, result reflect.Type) {
	if source.Size() != result.Size() {
		// TODO: debug
		// fmt.Println(newType.Size(), newType)
		// for i := 0; i < newType.NumField(); i++ {
		// 	fmt.Println(newType.Field(i))
		// }
		// fmt.Println(structType.Size(), structType)
		// for i := 0; i < structType.NumField(); i++ {
		// 	fmt.Println(structType.Field(i))
		// }
		panic("tags.Map: Unexpected case - type has a size different from size of original type")
	}
}

var structTypeConstructorBugWasFixed bool

func init() {
	switch {
	case strings.HasPrefix(runtime.Version(), "go1.7"):
		// there is bug in reflect.StructOf
	default:
		structTypeConstructorBugWasFixed = true
	}
}
