// Copyright (c) 2026 Ferdinando Ametrano <ferdinando@ametrano.net>
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
// * Redistributions of source code must retain the above copyright notice, this
//   list of conditions and the following disclaimer.
//
// * Redistributions in binary form must reproduce the above copyright notice,
//   this list of conditions and the following disclaimer in the documentation
//   and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

//go:build unix

package rotator

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNewWithMode(t *testing.T) {
	old := syscall.Umask(022)
	defer syscall.Umask(old)

	const mode = 0600
	name := filepath.Join(t.TempDir(), "test.log")
	r, err := NewWithMode(name, 1, false, 0, mode)
	if err != nil {
		t.Fatal(err)
	}

	// Write more than the 1000 byte threshold to force a rotation.
	line := make([]byte, 1100)
	for i := range line {
		line[i] = 'x'
	}
	line[len(line)-1] = '\n'
	if _, err := r.Write(line); err != nil {
		t.Fatal(err)
	}
	// Close waits for the compression to finish.
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{name, name + ".1.gz"} {
		fi, err := os.Stat(n)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != mode {
			t.Errorf("%s: mode %v, want %v", filepath.Base(n), perm, os.FileMode(mode))
		}
	}
}
