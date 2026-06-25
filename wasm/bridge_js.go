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
	"syscall/js"
)

func jsErr(msg string) js.Value {
	return js.Global().Get("Error").New(msg)
}

// promisify returns a JS function that yields a Promise. prepare runs
// synchronously so it can safely read JS args; its returned thunk runs on a
// goroutine so blocking Go work never stalls the single-threaded JS event loop.
func promisify(prepare func([]js.Value) func() (any, error)) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		work := prepare(args)
		var executor js.Func
		executor = js.FuncOf(func(_ js.Value, p []js.Value) any {
			resolve, reject := p[0], p[1]
			go func() {
				defer executor.Release()
				defer func() {
					if r := recover(); r != nil {
						reject.Invoke(jsErr(fmt.Sprintf("panic: %v", r)))
					}
				}()
				res, err := work()
				if err != nil {
					reject.Invoke(jsErr(err.Error()))
					return
				}
				resolve.Invoke(res)
			}()
			return nil
		})
		return js.Global().Get("Promise").New(executor)
	})
}

// await blocks the calling goroutine until the JS promise settles. It is safe
// only inside a goroutine (not a js.FuncOf callback), where a blocked goroutine
// yields control back to the JS event loop.
func await(promise js.Value) (js.Value, error) {
	done := make(chan struct{})
	var result js.Value
	var failure string
	var failed bool

	onOK := js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			result = a[0]
		}
		close(done)
		return nil
	})
	defer onOK.Release()
	onErr := js.FuncOf(func(_ js.Value, a []js.Value) any {
		failed = true
		if len(a) > 0 {
			failure = a[0].Call("toString").String()
		}
		close(done)
		return nil
	})
	defer onErr.Release()

	promise.Call("then", onOK).Call("catch", onErr)
	<-done
	if failed {
		return js.Value{}, fmt.Errorf("%s", failure)
	}
	return result, nil
}
