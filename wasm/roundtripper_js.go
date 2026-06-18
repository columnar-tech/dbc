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
	"bytes"
	"fmt"
	"io"
	"net/http"
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

	bufVal, err := await(respVal.Call("arrayBuffer"))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	arr := js.Global().Get("Uint8Array").New(bufVal)
	data := make([]byte, arr.Get("length").Int())
	js.CopyBytesToGo(data, arr)

	header := http.Header{}
	collect := js.FuncOf(func(_ js.Value, a []js.Value) any {
		header.Add(a[1].String(), a[0].String())
		return nil
	})
	respVal.Get("headers").Call("forEach", collect)
	collect.Release()

	status := respVal.Get("status").Int()
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, respVal.Get("statusText").String()),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentLength: int64(len(data)),
		Request:       req,
	}, nil
}
