package steam

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Validation struct{ AccountID, OwnerID, Lease uint64 }
type request struct {
	operation, value string
	result           chan response
}
type response struct {
	fields []string
	err    error
}

type Helper struct {
	command      *exec.Cmd
	input        io.WriteCloser
	queue        chan request
	ready        chan uint64
	done, exited chan struct{}
	stop         sync.Once
	mu           sync.Mutex
	pending      map[uint64]chan response
	serverID     uint64
}

func StartHelper(ctx context.Context, executable, library string) (*Helper, error) {
	if executable == "" {
		return nil, errors.New("Steam helper executable is required")
	}
	args := []string{"--timeout", "30"}
	if library != "" {
		args = append(args, "--library", library)
	}
	cmd := exec.Command(executable, args...)
	hideHelper(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("cannot create Steam helper input")
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, errors.New("cannot create Steam helper output")
	}
	// Steam's own diagnostics may contain account IDs. They are never relayed.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, errors.New("cannot start Steam helper")
	}
	h := &Helper{command: cmd, input: input, queue: make(chan request, 64), ready: make(chan uint64, 1),
		done: make(chan struct{}), exited: make(chan struct{}), pending: map[uint64]chan response{}}
	go h.write()
	go func() { h.read(output); _ = cmd.Wait(); close(h.exited) }()
	select {
	case id := <-h.ready:
		h.serverID = id
		return h, nil
	case <-ctx.Done():
		h.Close()
		return nil, errors.New("Steam helper startup timed out")
	case <-h.done:
		h.Close()
		return nil, errors.New("Steam helper initialization or logon failed")
	}
}

func (h *Helper) fail() {
	h.stop.Do(func() { close(h.done); _ = h.input.Close() })
}

func (h *Helper) read(output io.Reader) {
	defer h.fail()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 1024), 8192)
	ready := false
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 || fields[0] != "OSW1" {
			return
		}
		if fields[1] == "READY" && !ready && len(fields) == 3 {
			id, err := decimalID(fields[2])
			if err != nil {
				return
			}
			ready = true
			h.ready <- id
			continue
		}
		if !ready || fields[1] != "RESULT" || len(fields) < 4 {
			return
		}
		id, err := decimalID(fields[2])
		if err != nil {
			return
		}
		h.mu.Lock()
		result := h.pending[id]
		delete(h.pending, id)
		h.mu.Unlock()
		if result == nil {
			return
		}
		result <- response{fields: fields[3:]}
	}
}

func (h *Helper) write() {
	var sequence uint64
	for {
		select {
		case <-h.done:
			return
		case request := <-h.queue:
			sequence++
			h.mu.Lock()
			h.pending[sequence] = request.result
			h.mu.Unlock()
			_, err := fmt.Fprintf(h.input, "OSW1\t%d\t%s\t%s\n", sequence, request.operation, request.value)
			if err != nil {
				h.fail()
				return
			}
		}
	}
}

func (h *Helper) call(ctx context.Context, operation, value string) ([]string, error) {
	request := request{operation: operation, value: value, result: make(chan response, 1)}
	select {
	case <-h.done:
		return nil, errors.New("Steam helper unavailable")
	case <-ctx.Done():
		return nil, errors.New("Steam helper request timed out")
	case h.queue <- request:
	}
	select {
	case result := <-request.result:
		return result.fields, result.err
	case <-h.done:
		return nil, errors.New("Steam helper unavailable")
	case <-ctx.Done():
		// A timed-out validation may have acquired a native session. Terminate
		// this helper generation so no abandoned session survives the request.
		h.fail()
		_ = h.command.Process.Kill()
		return nil, errors.New("Steam helper request timed out")
	}
}

func (h *Helper) Validate(ctx context.Context, token string) (Validation, error) {
	if _, err := ParseToken(token); err != nil {
		return Validation{}, err
	}
	fields, err := h.call(ctx, "VALIDATE", token)
	if err != nil {
		return Validation{}, err
	}
	if len(fields) != 4 || fields[0] != "OK" {
		return Validation{}, errors.New("Steam validation refused")
	}
	var result Validation
	for i, dest := range []*uint64{&result.AccountID, &result.OwnerID, &result.Lease} {
		*dest, err = decimalID(fields[i+1])
		if err != nil {
			h.fail()
			return Validation{}, errors.New("invalid Steam helper result")
		}
	}
	return result, nil
}

func (h *Helper) End(lease uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.call(ctx, "END", strconv.FormatUint(lease, 10))
}

func (h *Helper) ServerID() uint64 { return h.serverID }
func (h *Helper) Close() error {
	h.fail()
	select {
	case <-h.exited:
		return nil
	case <-time.After(5 * time.Second):
		_ = h.command.Process.Kill()
		select {
		case <-h.exited:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("Steam helper did not exit")
		}
	}
}
