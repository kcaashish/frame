/*
 * Copyright 2022 Frame Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
	"context"
	"errors"
	"github.com/sujit-baniya/frame/middlewares/server/recovery"
	"github.com/sujit-baniya/log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sujit-baniya/frame/pkg/common/config"
	"github.com/sujit-baniya/frame/pkg/route"
)

// Frame is the core struct of frame.
type Frame struct {
	*route.Engine
	signalWaiter func(err chan error) error
}

// New creates a frame instance without any default config.
func New(opts ...config.Option) *Frame {
	options := config.NewOptions(opts)
	h := &Frame{
		Engine: route.NewEngine(options),
	}
	return h
}

// Default creates a frame instance with default middlewares.
func Default(opts ...config.Option) *Frame {
	h := New(opts...)
	h.Use(recovery.New())

	return h
}

// Spin runs the server until catching os.Signal or error returned by h.Run().
func (h *Frame) Spin() {
	errCh := make(chan error)
	h.initOnRunHooks(errCh)
	go func() {
		errCh <- h.Run()
	}()

	signalWaiter := waitSignal
	if h.signalWaiter != nil {
		signalWaiter = h.signalWaiter
	}

	if err := signalWaiter(errCh); err != nil {
		log.Error().Str("log_service", "HTTP Server").Msgf("Receive close signal: error=%v", err)
		if err := h.Engine.Close(); err != nil {
			log.Error().Str("log_service", "HTTP Server").Msgf("Close error=%v", err)
		}
		return
	}

	log.Info().Str("log_service", "HTTP Server").Msgf("Begin graceful shutdown, wait at most num=%d seconds...", h.GetOptions().ExitWaitTimeout/time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), h.GetOptions().ExitWaitTimeout)
	defer cancel()

	if err := h.Shutdown(ctx); err != nil {
		log.Error().Str("log_service", "HTTP Server").Msgf("Shutdown error=%v", err)
	}
}

// SetCustomSignalWaiter sets the signal waiter function.
// If Default one is not met the requirement, set this function to customize.
// Frame will exit immediately if f returns an error, otherwise it will exit gracefully.
func (h *Frame) SetCustomSignalWaiter(f func(err chan error) error) {
	h.signalWaiter = f
}

// Default implementation for signal waiter.
// SIGTERM triggers immediately close.
// SIGHUP|SIGINT triggers graceful shutdown.
func waitSignal(errCh chan error) error {
	signalToNotify := []os.Signal{syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM}
	if signal.Ignored(syscall.SIGHUP) {
		signalToNotify = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, signalToNotify...)

	select {
	case sig := <-signals:
		switch sig {
		case syscall.SIGTERM:
			// force exit
			return errors.New(sig.String()) // nolint
		case syscall.SIGHUP, syscall.SIGINT:
			log.Info().Str("log_service", "HTTP Server").Msgf("Received signal: %s\n", sig)
			// graceful shutdown
			return nil
		}
	case err := <-errCh:
		// error occurs, exit immediately
		return err
	}

	return nil
}

func (h *Frame) initOnRunHooks(errChan chan error) {
	// add register func to runHooks
	opt := h.GetOptions()
	h.OnRun = append(h.OnRun, func(ctx context.Context) error {
		go func() {
			// delay register 1s
			time.Sleep(1 * time.Second)
			if err := opt.Registry.Register(opt.RegistryInfo); err != nil {
				log.Error().Str("log_service", "HTTP Server").Msgf("Register error=%v", err)
				// pass err to errChan
				errChan <- err
			}
		}()
		return nil
	})
}
