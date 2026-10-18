package bd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrLedgerStale is a ledger write refused because the ledger changed after it was read.
var ErrLedgerStale = errors.New("ledger changed since it was read")

func ledgerDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "bt", "ledgers")
	return dir, os.MkdirAll(dir, 0o755)
}

// UpdateLedger rewrites a ledger's description with edit(current). Every session may write a
// ledger, so writes of one ledger take turns under a lock and edit always sees what bd holds now.
// shortcut: the lock is a local flock, so writes from other machines or a plain bd update skip it;
// move it into bd if ledgers get written from more than one host.
func (c Client) UpdateLedger(id string, edit func(cur string) (string, error)) error {
	dir, err := ledgerDir()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	is, err := c.Show(id)
	if err != nil {
		return err
	}
	if !is.IsLedger() {
		return fmt.Errorf("%s is not a live ledger (pinned, label live)", id)
	}
	after, err := edit(is.Description)
	if err != nil || after == is.Description {
		return err
	}
	return c.ExecInput(after, "update", id, "--body-file", "-")
}

// ReplaceLedger writes after only when bd still holds before, so no update made in between is lost.
func (c Client) ReplaceLedger(id, before, after string) error {
	return c.UpdateLedger(id, func(cur string) (string, error) {
		if cur != before {
			return "", fmt.Errorf("%s: %w", id, ErrLedgerStale)
		}
		return after, nil
	})
}

// LedgerCheckout writes the ledger's description to a fresh file, plus a .base copy that
// LedgerCommit checks against, and returns the file's path.
func (c Client) LedgerCheckout(id string) (string, error) {
	is, err := c.Show(id)
	if err != nil {
		return "", err
	}
	if !is.IsLedger() {
		return "", fmt.Errorf("%s is not a live ledger (pinned, label live)", id)
	}
	dir, err := ledgerDir()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, id+"-*.md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(is.Description); err != nil {
		return "", err
	}
	return f.Name(), os.WriteFile(f.Name()+".base", []byte(is.Description), 0o644)
}

// LedgerCommit writes a checked-out ledger back unless it changed since the checkout. A stale
// checkout keeps the edited file, to redo the edit from, and loses its .base, so it cannot be committed.
func (c Client) LedgerCommit(path string) error {
	name := filepath.Base(path)
	i := strings.LastIndex(name, "-")
	if i < 0 || !strings.HasSuffix(name, ".md") {
		return fmt.Errorf("%s is not a bt ledger checkout", path)
	}
	base, err := os.ReadFile(path + ".base")
	if err != nil {
		return fmt.Errorf("%s is not a bt ledger checkout: %w", path, err)
	}
	mine, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := c.ReplaceLedger(name[:i], string(base), string(mine)); err != nil {
		if errors.Is(err, ErrLedgerStale) {
			err = errors.Join(fmt.Errorf("%w; your edit is in %s, check it out again and redo it", err, path), os.Remove(path+".base"))
		}
		return err
	}
	return errors.Join(os.Remove(path), os.Remove(path+".base"))
}
