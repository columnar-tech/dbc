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

//go:build js

package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall/js"
)

// fetchRoundTripper implements http.RoundTripper by delegating to the host's JS
// fetch(); Go's own network stack is unavailable/disabled under Node js/wasm.
type fetchRoundTripper struct{}

func (fetchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	opts := map[string]any{"method": req.Method}

	headers := map[string]any{}
	for k, v := range req.Header {
		headers[k] = strings.Join(v, ", ")
	}
	opts["headers"] = headers

	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		if len(body) > 0 {
			buf := js.Global().Get("Uint8Array").New(len(body))
			js.CopyBytesToJS(buf, body)
			opts["body"] = buf
		}
	}

	respVal, err := await(js.Global().Call("fetch", req.URL.String(), js.ValueOf(opts)))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", req.URL, err)
	}

	header := http.Header{}
	collect := js.FuncOf(func(_ js.Value, a []js.Value) any {
		header.Add(a[1].String(), a[0].String())
		return nil
	})
	respVal.Get("headers").Call("forEach", collect)
	collect.Release()

	status := respVal.Get("status").Int()
	resp := &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, respVal.Get("statusText").String()),
		Header:        header,
		ContentLength: parseContentLength(header),
		Request:       req,
	}

	// Stream the body via the ReadableStream reader rather than buffering the
	// whole response, so large driver tarballs are copied to disk chunk-by-chunk
	// instead of being held in wasm linear memory.
	body := respVal.Get("body")
	if body.IsNull() || body.IsUndefined() {
		resp.Body = http.NoBody
	} else {
		resp.Body = &jsStreamBody{reader: body.Call("getReader")}
	}
	return resp, nil
}

func parseContentLength(h http.Header) int64 {
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			return n
		}
	}
	return -1
}

// jsStreamBody adapts a JS ReadableStreamDefaultReader to an io.ReadCloser. Read
// awaits, so it must run on a goroutine (not a js.FuncOf callback).
type jsStreamBody struct {
	reader js.Value
	buf    []byte
	done   bool
}

func (b *jsStreamBody) Read(p []byte) (int, error) {
	for len(b.buf) == 0 {
		if b.done {
			return 0, io.EOF
		}
		res, err := await(b.reader.Call("read"))
		if err != nil {
			return 0, err
		}
		if res.Get("done").Bool() {
			b.done = true
			continue
		}
		chunk := res.Get("value")
		n := chunk.Get("length").Int()
		if n == 0 {
			continue
		}
		b.buf = make([]byte, n)
		js.CopyBytesToGo(b.buf, chunk)
	}
	n := copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

func (b *jsStreamBody) Close() error {
	if !b.done {
		_, _ = await(b.reader.Call("cancel"))
	}
	return nil
}
