// Copyright (c) 2017, moshee
// Copyright (c) 2017, 2024 Josh Rickmar <jrick@zettaport.com>
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

// Package rotator implements a simple logfile rotator. Logs are read from an
// io.Reader and are written to a file until they reach a specified size. The
// log is then truncated and (by default) gzipped to another file or
// compressed with a user-configurable compression scheme.
package rotator

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// nl is a byte slice containing a newline byte.  It is used to avoid creating
// additional allocations when writing newlines to the log file.
var nl = []byte{'\n'}

// A Rotator writes input to a file, splitting it up into gzipped chunks once
// the filesize reaches a certain threshold.
type Rotator struct {
	size      int64
	threshold int64
	maxRolls  int
	filename  string
	out       *os.File
	tee       bool
	zw        Compressor
	zSuffix   string
	zMu       sync.Mutex
	wg        sync.WaitGroup
}

// New returns a new Rotator.  The rotator can be used either by reading input
// from an io.Reader by calling Run, or writing directly to the Rotator with
// Write.
func New(filename string, thresholdKB int64, tee bool, maxRolls int) (*Rotator, error) {
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	return &Rotator{
		size:      stat.Size(),
		threshold: 1000 * thresholdKB,
		maxRolls:  maxRolls,
		filename:  filename,
		out:       f,
		tee:       tee,
		zw:        gzip.NewWriter(nil),
		zSuffix:   "gz",
	}, nil
}

// Compressor writes a compressed stream to an underlying writer.  The
// underlying writer and the compression state is reset for each rotated file.
type Compressor interface {
	io.WriteCloser
	Reset(io.Writer)
	Flush() error
}

// SetCompressor changes the algorithm or compression parameters used to
// compress rotated log files.  By default, compression is performed using
// gzip with default parameters and a "gz" suffix.  Setting a nil compressor
// disables compression.
//
// SetCompressor is not concurrent safe and must be called before the Rotator
// is run.
func (r *Rotator) SetCompressor(zw Compressor, suffix string) {
	r.zw = zw
	r.zSuffix = strings.TrimPrefix(suffix, ".")
}

// Run begins reading lines from the reader and rotating logs as necessary.  Run
// should not be called concurrently with Write.
//
// Prefer to use Rotator as a writer instead to avoid unnecessary scanning of
// input, as this job is better handled using io.Pipe.
func (r *Rotator) Run(reader io.Reader) error {
	in := bufio.NewReader(reader)

	// Rotate file immediately if it is already over the size limit.
	if r.size >= r.threshold {
		if err := r.rotate(); err != nil {
			return err
		}
		r.size = 0
	}

	for {
		line, isPrefix, err := in.ReadLine()
		if err != nil {
			return err
		}

		n, _ := r.out.Write(line)
		r.size += int64(n)
		if r.tee {
			os.Stdout.Write(line)
		}
		if isPrefix {
			continue
		}

		m, _ := r.out.Write(nl)
		if r.tee {
			os.Stdout.Write(nl)
		}
		r.size += int64(m)

		if r.size >= r.threshold {
			err := r.rotate()
			if err != nil {
				return err
			}
			r.size = 0
		}
	}
}

// Write implements the io.Writer interface for Rotator.  If p ends in a newline
// and the file has exceeded the threshold size, the file is rotated.
func (r *Rotator) Write(p []byte) (n int, err error) {
	n, _ = r.out.Write(p)
	r.size += int64(n)

	if r.size >= r.threshold && len(p) > 0 && p[len(p)-1] == '\n' {
		err := r.rotate()
		if err != nil {
			return 0, err
		}
		r.size = 0
	}

	return n, nil
}

// Close closes the output logfile.
func (r *Rotator) Close() error {
	err := r.out.Close()
	r.wg.Wait()
	return err
}

func (r *Rotator) rotate() error {
	dir := filepath.Dir(r.filename)
	glob := filepath.Join(dir, filepath.Base(r.filename)+".*")
	existing, err := filepath.Glob(glob)
	if err != nil {
		return err
	}

	maxNum := 0
	for _, name := range existing {
		parts := strings.Split(name, ".")
		if len(parts) < 2 {
			continue
		}
		numIdx := len(parts) - 1
		if parts[numIdx] == r.zSuffix {
			numIdx--
		}
		num, err := strconv.Atoi(parts[numIdx])
		if err != nil {
			continue
		}
		if num > maxNum {
			maxNum = num
		}
	}

	err = r.out.Close()
	if err != nil {
		return err
	}
	rotname := fmt.Sprintf("%s.%d", r.filename, maxNum+1)
	err = os.Rename(r.filename, rotname)
	if err != nil {
		return err
	}
	if r.maxRolls > 0 {
		for n := maxNum + 1 - r.maxRolls; n >= 1; n-- {
			var name string
			if r.zw == nil || r.zSuffix == "" {
				name = fmt.Sprintf("%s.%d", r.filename, n)
			} else {
				name = fmt.Sprintf("%s.%d.%s", r.filename, n, r.zSuffix)
			}
			err := os.Remove(name)
			if err != nil {
				break
			}
		}
	}
	r.out, err = os.OpenFile(r.filename, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	if r.zw != nil {
		r.wg.Add(1)
		go func() {
			r.zMu.Lock()
			defer r.zMu.Unlock()

			err := r.compress(rotname)
			if err == nil {
				os.Remove(rotname)
			}
			r.wg.Done()
		}()
	}

	return nil
}

func (r *Rotator) compress(name string) (err error) {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()

	zname := fmt.Sprintf("%s.%s", name, r.zSuffix)
	arc, err := os.OpenFile(zname, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if e := arc.Close(); err == nil {
			err = e
		}
	}()

	r.zw.Reset(arc)
	defer func() {
		if e := r.zw.Close(); err == nil {
			err = e
		}
	}()

	_, err = io.Copy(r.zw, f)
	return err
}
