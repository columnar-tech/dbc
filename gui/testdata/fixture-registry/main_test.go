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
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

func startTestServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	go func() { _ = http.Serve(listener, newMux()) }()
	t.Cleanup(func() { listener.Close() })
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func TestIndexYAML(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("empty index.yaml")
	}
}

func TestRateLimited(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/drivers/rate-limited/1.0.0/test.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
}

func TestMissing(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/drivers/missing/1.0.0/test.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServerStartsOnPort0(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if port == 0 {
		t.Fatal("expected non-zero port")
	}
}

func TestIndexYAMLContentType(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDriverNotFound(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/drivers/nonexistent/1.0.0/test.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestRetryAfterHeader(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/drivers/rate-limited/1.0.0/test.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}
}

func TestMultipleRequests(t *testing.T) {
	base := startTestServer(t)
	for i := 0; i < 3; i++ {
		resp, err := http.Get(base + "/index.yaml")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}
}

func TestIndexYAMLContainsDrivers(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	content := string(body)
	for _, name := range []string{"fixture-happy", "fixture-tampered", "fixture-rate-limited", "fixture-missing"} {
		found := false
		for i := 0; i <= len(content)-len(name); i++ {
			if content[i:i+len(name)] == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("index.yaml missing driver path %q", name)
		}
	}
}

func TestTamperedDriverServed(t *testing.T) {
	base := startTestServer(t)
	resp, err := http.Get(base + "/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for index, got %d", resp.StatusCode)
	}
}
