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

package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	port := flag.Int("port", 8080, "Port to listen on (0 for random)")
	outputPortFile := flag.String("output-port-file", "", "Write chosen port to this file")
	flag.Parse()

	mux := newMux()

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
		os.Exit(1)
	}

	chosenPort := listener.Addr().(*net.TCPAddr).Port
	fmt.Printf("Listening on port %d\n", chosenPort)

	if *outputPortFile != "" {
		if err := os.WriteFile(*outputPortFile, []byte(fmt.Sprintf("%d", chosenPort)), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write port file: %v\n", err)
		}
	}

	if err := http.Serve(listener, mux); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()

	// runtime.Caller(0) embeds this source file's path at compile time, giving
	// us a stable absolute path to fixtures/ regardless of working directory.
	_, filename, _, _ := runtime.Caller(0)
	fd := filepath.Join(filepath.Dir(filename), "fixtures")

	mux.HandleFunc("GET /index.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(fd, "index.yaml"))
	})

	mux.HandleFunc("GET /drivers/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[len("/drivers/"):]

		if strings.Contains(path, "rate-limited") {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		if strings.Contains(path, "missing") {
			http.NotFound(w, r)
			return
		}

		http.ServeFile(w, r, filepath.Join(fd, "drivers", path))
	})

	return mux
}
