package stream

import (
	"context"
	"io"

	"github.com/containerd/log"
	"github.com/moby/moby/v2/pkg/pools"
	"github.com/moby/term"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

var defaultEscapeSequence = []byte{16, 17} // ctrl-p, ctrl-q

// AttachConfig is the config struct used to attach a client to a stream's stdio
type AttachConfig struct {
	// Tells the attach copier that the stream's stdin is a TTY and to look for
	// escape sequences in stdin to detach from the stream.
	// When true the escape sequence is not passed to the underlying stream
	TTY bool
	// Specifies the detach keys the client will be using
	// Only useful when `TTY` is true
	DetachKeys []byte

	// CloseStdin signals that once done, stdin for the attached stream should be closed
	// For example, this would close the attached container's stdin.
	CloseStdin bool

	// UseStd* indicate whether the client has requested to be connected to the
	// given stream or not.  These flags are used instead of checking Std* != nil
	// at points before the client streams Std* are wired up.
	UseStdin, UseStdout, UseStderr bool

	// CStd* are the streams directly connected to the container
	CStdin           io.WriteCloser
	CStdout, CStderr io.ReadCloser

	// Provide client streams to wire up to
	Stdin          io.ReadCloser
	Stdout, Stderr io.Writer
}

// AttachStreams attaches the container's streams to the AttachConfig
func (c *Config) AttachStreams(cfg *AttachConfig) {
	if cfg.UseStdin {
		cfg.CStdin = c.StdinPipe()
	}

	if cfg.UseStdout {
		cfg.CStdout = c.StdoutPipe()
	}

	if cfg.UseStderr {
		cfg.CStderr = c.StderrPipe()
	}
}

// CopyStreams starts goroutines to copy data in and out to/from the container
func (c *Config) CopyStreams(ctx context.Context, cfg *AttachConfig) <-chan error {
	var group errgroup.Group

	// Connect stdin of container to the attach stdin stream.
	if cfg.Stdin != nil {
		group.Go(func() error {
			log.G(ctx).Debug("attach: stdin: begin")
			defer log.G(ctx).Debug("attach: stdin: end")

			defer func() {
				if cfg.CloseStdin && !cfg.TTY {
					cfg.CStdin.Close()
				} else {
					// No matter what, when stdin is closed (io.Copy unblock), close stdout and stderr
					if cfg.CStdout != nil {
						cfg.CStdout.Close()
					}
					if cfg.CStderr != nil {
						cfg.CStderr.Close()
					}
				}
			}()

			var err error
			if cfg.TTY {
				_, err = copyEscapable(cfg.CStdin, cfg.Stdin, cfg.DetachKeys)
			} else {
				_, err = pools.Copy(cfg.CStdin, cfg.Stdin)
			}
			if errors.Is(err, io.ErrClosedPipe) {
				err = nil
			}
			if err != nil {
				log.G(ctx).WithError(err).Debug("error on attach stdin")
				return errors.Wrap(err, "error on attach stdin")
			}
			return nil
		})
	}

	attachStream := func(name string, stream io.Writer, streamPipe io.ReadCloser) error {
		log.G(ctx).Debugf("attach: %s: begin", name)
		defer log.G(ctx).Debugf("attach: %s: end", name)
		defer func() {
			// Make sure stdin gets closed
			if cfg.Stdin != nil {
				cfg.Stdin.Close()
			}
			streamPipe.Close()
		}()

		_, err := pools.Copy(stream, streamPipe)
		if errors.Is(err, io.ErrClosedPipe) {
			err = nil
		}
		if err != nil {
			log.G(ctx).WithError(err).Debugf("attach: %s", name)
			return errors.Wrapf(err, "error attaching %s stream", name)
		}
		return nil
	}

	if cfg.Stdout != nil {
		group.Go(func() error {
			return attachStream("stdout", cfg.Stdout, cfg.CStdout)
		})
	}
	if cfg.Stderr != nil {
		group.Go(func() error {
			return attachStream("stderr", cfg.Stderr, cfg.CStderr)
		})
	}

	errs := make(chan error, 1)
	go func() {
		defer log.G(ctx).Debug("attach done")
		groupErr := make(chan error, 1)
		go func() {
			groupErr <- group.Wait()
		}()
		select {
		case <-ctx.Done():
			// close all pipes
			if cfg.CStdin != nil {
				cfg.CStdin.Close()
			}
			if cfg.CStdout != nil {
				cfg.CStdout.Close()
			}
			if cfg.CStderr != nil {
				cfg.CStderr.Close()
			}

			if cfg.Stdin != nil {
				// In this case, `cfg.Stdin` is a stream from the client.
				// The way `io.Copy` works we may get stuck waiting to read from `cfg.Stdin` even if the container has exited.
				// This will cause the `io.Copy` to never return and the `group.Wait()` to never return.
				// By closing cfg.Stdin we will cause the `io.Copy` to return and the `group.Wait()` to return.
				cfg.Stdin.Close()
			}

			// Now with these closed, wait should return.
			if err := group.Wait(); err != nil {
				errs <- err
				return
			}
			errs <- ctx.Err()
		case err := <-groupErr:
			errs <- err
		}
	}()

	return errs
}

func copyEscapable(dst io.Writer, src io.ReadCloser, keys []byte) (written int64, _ error) {
	if len(keys) == 0 {
		keys = defaultEscapeSequence
	}
	pr := term.NewEscapeProxy(src, keys)
	defer src.Close()

	return pools.Copy(dst, pr)
}
