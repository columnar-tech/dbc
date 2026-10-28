// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build cgo

package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/api"
)

// object is a Go value exposed to C as an opaque pointer.
type object interface{ free() }

// newObject returns a C allocation holding a cgo.Handle to v. C treats it as
// an opaque struct pointer and never dereferences it, so no Go pointer
// crosses into C.
func newObject(v object) unsafe.Pointer {
	p := C.malloc(C.size_t(unsafe.Sizeof(C.uintptr_t(0))))
	*(*C.uintptr_t)(p) = C.uintptr_t(cgo.NewHandle(v))
	return p
}

// objectAt returns the object behind p, or nil for NULL or an object of
// another type.
func objectAt[T object](p unsafe.Pointer) T {
	var zero T
	if p == nil {
		return zero
	}
	v, _ := cgo.Handle(*(*C.uintptr_t)(p)).Value().(T)
	return v
}

// freeObject releases the object behind p and the C strings it owns. NULL is
// a no-op.
func freeObject(p unsafe.Pointer) {
	if p == nil {
		return
	}
	h := cgo.Handle(*(*C.uintptr_t)(p))
	h.Value().(object).free()
	h.Delete()
	C.free(p)
}

type errorObj struct{ message *C.char }

func newErrorObj(err error) *errorObj { return &errorObj{message: C.CString(err.Error())} }
func (o *errorObj) free()             { C.free(unsafe.Pointer(o.message)) }

type clientObj struct{ client *dbc.Client }

func (*clientObj) free() {}

// record maps dbc_field ids to C strings; a field that doesn't apply is absent.
type record map[int32]*C.char

func (r record) free() {
	for _, p := range r {
		C.free(unsafe.Pointer(p))
	}
}

// listObj is every operation's result: rows of driver fields plus list-level
// fields (dbc_list_info).
type listObj struct {
	rows []record
	info record
}

func (l *listObj) free() {
	for _, r := range l.rows {
		r.free()
	}
	l.info.free()
}

func searchList(r api.SearchResult) *listObj {
	l := &listObj{info: record{}}
	if r.Warning != "" {
		l.info[fieldWarning] = C.CString(r.Warning)
	}
	for _, d := range r.Drivers {
		l.rows = append(l.rows, record{
			fieldID:          C.CString(d.Path),
			fieldName:        C.CString(d.Title),
			fieldLicense:     C.CString(d.License),
			fieldDescription: C.CString(d.Description),
		})
	}
	return l
}

func resolveList(r api.ResolveResult) *listObj {
	l := &listObj{info: record{fieldID: C.CString(r.Path), fieldPlatform: C.CString(r.Platform)}}
	if r.Latest != nil {
		l.info[fieldLatestVersion] = C.CString(r.Latest.Version)
		l.info[fieldLatestURL] = C.CString(r.Latest.URL)
	}
	for _, v := range r.Versions {
		l.rows = append(l.rows, record{fieldVersion: C.CString(v)})
	}
	return l
}

func manifestList(m api.Manifest) *listObj {
	return &listObj{rows: []record{{
		fieldID:         C.CString(m.ID),
		fieldName:       C.CString(m.Name),
		fieldVersion:    C.CString(m.Version),
		fieldSource:     C.CString(m.Source),
		fieldDriverPath: C.CString(m.DriverPath),
	}}}
}

func installedList(ds []api.InstalledDriver) *listObj {
	l := &listObj{}
	for _, d := range ds {
		l.rows = append(l.rows, record{
			fieldID:       C.CString(d.ID),
			fieldName:     C.CString(d.Name),
			fieldVersion:  C.CString(d.Version),
			fieldLocation: C.CString(d.FilePath),
		})
	}
	return l
}
