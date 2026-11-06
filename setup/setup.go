// SPDX-License-Identifier: AGPL-3.0-or-later

// Package setup supplies the common, host-authorized first-owner browser flow.
// Applications own account validation and an atomic first-owner transaction.
package setup

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/airencracken/comfylib/keyfile"
)

// ErrComplete means that an owner already exists; browser setup stays closed.
var ErrComplete = errors.New("this instance already has an owner; sign in or use create-owner to add another")

// Owner is the first account's chosen credentials.
type Owner struct{ Username, Password string }

// Config connects the shared flow to one application's account transaction.
type Config struct {
	App, Directory, BaseURL, Address string
	// Ready reports whether any owner exists, including suspended owners.
	Ready func(context.Context) (bool, error)
	// Create must atomically refuse if any owner exists before inserting the first.
	// It must not promote or overwrite an existing account. Return ErrComplete
	// when another process won the race to create the first owner.
	Create func(context.Context, Owner) error
}

// Handler provides setup pages and the command that reveals their private key.
type Handler struct {
	config   Config
	complete atomic.Bool
}

// New validates configuration without creating files.
func New(c Config) (*Handler, error) {
	if c.App == "" || strings.Trim(c.App, "abcdefghijklmnopqrstuvwxyz") != "" || c.Directory == "" || c.Ready == nil || c.Create == nil {
		return nil, errors.New("setup requires an app name, data directory, and account callbacks")
	}
	if _, err := c.location(); err != nil {
		return nil, err
	}
	return &Handler{config: c}, nil
}
func (c Config) location() (string, error) {
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
			return "", errors.New("setup base URL must be an HTTP or HTTPS origin")
		}
		return strings.TrimRight(c.BaseURL, "/") + "/setup", nil
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return "", errors.New("setup needs a base URL or listen address")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/setup", nil
}
func (h *Handler) ready(ctx context.Context) (bool, error) {
	if h.complete.Load() {
		return true, nil
	}
	ready, err := h.config.Ready(ctx)
	if err == nil && ready {
		h.complete.Store(true)
	}
	return ready, err
}
func (h *Handler) keyPath() string { return filepath.Join(h.config.Directory, ".setup.key") }
func (h *Handler) checkDirectory() error {
	if err := os.MkdirAll(h.config.Directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(h.config.Directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("setup data directory must be private (mode 0700)")
	}
	return nil
}
func (h *Handler) loadKey() ([]byte, error) {
	info, err := os.Lstat(h.keyPath())
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 65 {
		return nil, errors.New("setup key must be a private regular file")
	}
	data, err := os.ReadFile(h.keyPath())
	if err != nil {
		return nil, err
	}
	return keyfile.Decode(data, 32, keyfile.Hex)
}

// Enable prints an existing or newly created setup key and browser address.
// Keys are never logged, placed in URLs, or sent to the browser by this package.
// The key works only until an owner is created through the browser or CLI.
func (h *Handler) Enable(ctx context.Context, out io.Writer) error {
	ready, err := h.ready(ctx)
	if err != nil {
		return err
	}
	if ready {
		return ErrComplete
	}
	if err = h.checkDirectory(); err != nil {
		return err
	}
	if _, err = os.Lstat(h.keyPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err == nil {
		if _, err = h.loadKey(); err != nil {
			return err
		}
	}
	key, err := keyfile.LoadOrCreate(h.keyPath(), 32, keyfile.Hex)
	if err != nil {
		return err
	}
	ready, err = h.ready(ctx)
	if err != nil {
		return err
	}
	if ready {
		return ErrComplete
	}
	location, err := h.config.location()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Start %s if it is not running, then open:\n%s\n\nSetup key: %s\n\nEnter this key on the setup page to create your owner account.\nKeep it private. Setup closes when an owner exists.\n", h.config.App, location, hex.EncodeToString(key))
	return err
}

// Wait blocks background workers until setup has completed or ctx is canceled.
func (h *Handler) Wait(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, err := h.ready(ctx)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
