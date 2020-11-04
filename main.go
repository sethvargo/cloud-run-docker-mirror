// Copyright 2020 Cloud Run Docker Mirror Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/sethvargo/go-signalcontext"
)

var (
	stderr = os.Stderr
	stdout = os.Stdout
)

func main() {
	ctx, cancel := signalcontext.OnInterrupt()

	err := realMain(ctx)
	cancel()

	if err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(stdout, "server stopped\n")
}

func realMain(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/", handleMirror())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	errCh := make(chan error)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			select {
			case errCh <- fmt.Errorf("server terminated: %w", err):
			default:
			}
		}
	}()

	fmt.Fprintf(stdout, "server is listening on :%s\n", port)

	// Wait for the context to finish or the server to error
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		fmt.Fprint(stderr, "shutting down...\n")
	}

	// Shutdown the server
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(stopCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

type Mirror struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

func (m *Mirror) Name() string {
	return fmt.Sprintf("(%s to %s)", m.Src, m.Dst)
}

type MirrorError struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Err   string `json:"error"`
}

func (m *MirrorError) Error() string {
	return fmt.Sprintf("[%d] %s: %s", m.Index, m.Name, m.Err)
}

type Request struct {
	Mirrors     []*Mirror `json:"mirrors"`
	Parallelism int       `json:"parallelism"`
}

type Response struct {
	OK     bool           `json:"ok"`
	Errors []*MirrorError `json:"errors,omitempty"`
}

// handleMirror is the HTTP handler for mirroring.
func handleMirror() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := decodeJSON(w, r, &req); err != nil {
			var jerr *jsonError
			if errors.As(err, &jerr) {
				http.Error(w, jerr.msg, jerr.status)
				return
			}

			fmt.Fprintf(stderr, "%s\n", err)
			internalError(w, err)
			return
		}

		parallelism := req.Parallelism
		if parallelism == 0 {
			parallelism = 5
		}

		jobs := make(chan func() *MirrorError, len(req.Mirrors))
		results := make(chan *MirrorError, len(req.Mirrors))
		for i := 0; i < parallelism; i++ {
			go worker(jobs, results)
		}

		for i, m := range req.Mirrors {
			i := i
			m := m

			jobs <- func() *MirrorError {
				fmt.Fprintf(stdout, "[%d] %s processing\n", i, m.Name())
				defer fmt.Fprintf(stdout, "[%d] %s finished\n", i, m.Name())
				if err := gcrane.Copy(m.Src, m.Dst); err != nil {
					return &MirrorError{
						Index: i,
						Name:  m.Name(),
						Err:   err.Error(),
					}
				}
				return nil
			}
		}
		close(jobs)

		var errors []*MirrorError
		for i := 0; i < len(req.Mirrors); i++ {
			if err := <-results; err != nil {
				errors = append(errors, err)
			}
		}

		if len(errors) > 0 {
			jsonResponse(w, &Response{Errors: errors})
			return
		}

		jsonResponse(w, &Response{OK: true})
	})
}

func worker(jobs <-chan func() *MirrorError, results chan<- *MirrorError) {
	for j := range jobs {
		results <- j()
	}
}

// jsonResponse writes a JSON response to the response
func jsonResponse(w http.ResponseWriter, i interface{}) {
	b, err := json.Marshal(i)
	if err != nil {
		internalError(w, fmt.Errorf("failed to marshal json: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "%s\n", b)
}

// internalError returns an internal error response. If the provided error is
// not nil, it prints it to stderr (but not in the response).
func internalError(w http.ResponseWriter, err error) {
	if err != nil {
		fmt.Fprintf(stderr, "%s\n", err)
	}

	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

type jsonError struct {
	status int
	msg    string
}

func (e *jsonError) Error() string {
	return e.msg
}

// decodeJSON decodes the JSON in the request.
func decodeJSON(w http.ResponseWriter, r *http.Request, i interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1 MB

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&i); err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError

		switch {
		case errors.As(err, &syntaxError):
			msg := fmt.Sprintf("Request body contains badly-formed JSON (at position %d)", syntaxError.Offset)
			return &jsonError{http.StatusBadRequest, msg}
		case errors.Is(err, io.ErrUnexpectedEOF):
			msg := "Request body contains badly-formed JSON"
			return &jsonError{http.StatusBadRequest, msg}
		case errors.As(err, &unmarshalTypeError):
			msg := fmt.Sprintf("Request body contains an invalid value for the %q field (at position %d)", unmarshalTypeError.Field, unmarshalTypeError.Offset)
			return &jsonError{http.StatusBadRequest, msg}
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			msg := fmt.Sprintf("Request body contains unknown field %s", fieldName)
			return &jsonError{http.StatusBadRequest, msg}
		case errors.Is(err, io.EOF):
			msg := "Request body must not be empty"
			return &jsonError{http.StatusBadRequest, msg}
		case err.Error() == "http: request body too large":
			msg := "Request body must not be larger than 1MB"
			return &jsonError{http.StatusRequestEntityTooLarge, msg}
		default:
			return err
		}
	}

	return nil
}
