// Package jsonreference is shared test-fixture machinery. Production packages
// must not import it; it exposes no application state or runtime hooks.
package jsonreference

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Case selects a concrete JSON type and optionally its anonymous source shape.
type Case struct {
	Name  string
	Type  reflect.Type
	Shape string
}

// Check compares encoding and decoding with immutable synthetic JSON fixtures.
func Check(t *testing.T, cases []Case) {
	t.Helper()
	const path = "testdata/json_reference.json"
	expected := map[string]string{}
	{
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &expected); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Shape != "" {
				jsonReferenceShapeExists(t, tc.Shape)
			}
			for _, populated := range []bool{false, true} {
				name := tc.Name + "/zero"
				if populated {
					name = tc.Name + "/populated"
				}
				value := reflect.New(tc.Type).Elem()
				if populated {
					jsonReferenceValue(value)
				}
				encoded, err := json.Marshal(value.Interface())
				if err != nil {
					t.Fatal(err)
				}
				saved, ok := expected[name]
				if !ok {
					t.Fatal("missing JSON reference:", name)
				}
				if string(encoded) != saved {
					t.Fatal("JSON encoding changed:", name)
				}
				decoded := reflect.New(tc.Type)
				if err := json.Unmarshal([]byte(saved), decoded.Interface()); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(value.Interface(), decoded.Elem().Interface()) {
					t.Fatal("saved JSON decoded differently:", name)
				}
				var stream bytes.Buffer
				if err := json.NewEncoder(&stream).Encode(value.Interface()); err != nil {
					t.Fatal(err)
				}
				if stream.String() != saved+"\n" {
					t.Fatal("JSON encoder changed:", name)
				}
			}
		})
	}
}

// jsonReferenceValue deliberately populates every serialized field, including
// omitempty fields. Fixtures contain only synthetic data recorded on 06912ba.
func jsonReferenceValue(v reflect.Value) {
	if v.Type() == reflect.TypeOf(json.RawMessage{}) {
		v.SetBytes([]byte(`{"synthetic":7}`))
		return
	}
	if v.Type() == reflect.TypeOf(json.Number("")) {
		v.SetString("7")
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		v.SetString("fixture <&> \"value\"")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(7.25)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		jsonReferenceValue(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() && v.Type().Field(i).Tag.Get("json") != "-" {
				jsonReferenceValue(v.Field(i))
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			jsonReferenceValue(v.Index(i))
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		jsonReferenceValue(v.Index(0))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		k := reflect.New(v.Type().Key()).Elem()
		x := reflect.New(v.Type().Elem()).Elem()
		jsonReferenceValue(k)
		jsonReferenceValue(x)
		v.SetMapIndex(k, x)
	case reflect.Interface:
		v.Set(reflect.ValueOf(float64(7)))
	default:
		panic("unhandled JSON reference kind: " + v.Kind().String())
	}
}

// Anonymous wire structs cannot be named from a package test. Verify that their
// actual source shape still matches the test type before exercising its codec.
func jsonReferenceShapeExists(t *testing.T, want string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fs, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(*ast.StructType); ok {
				var b bytes.Buffer
				if err := format.Node(&b, fs, s); err != nil {
					t.Fatal(err)
				}
				found = found || b.String() == want
			}
			return true
		})
		if found {
			return
		}
	}
	t.Fatal("anonymous JSON source shape changed")
}
