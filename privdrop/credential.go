// SPDX-License-Identifier: AGPL-3.0-or-later

package privdrop

import (
	"fmt"
	"maps"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// lookup resolves accounts; tests replace its fields, and a nil field means
// os/user.
type lookup struct {
	user     func(name string) (*user.User, error)
	group    func(name string) (*user.Group, error)
	groupIDs func(*user.User) ([]string, error)
}

func (l lookup) lookupUser(name string) (*user.User, error) {
	if l.user != nil {
		return l.user(name)
	}
	return user.Lookup(name)
}

func (l lookup) lookupGroup(name string) (*user.Group, error) {
	if l.group != nil {
		return l.group(name)
	}
	return user.LookupGroup(name)
}

func (l lookup) supplementary(account *user.User) ([]string, error) {
	if l.groupIDs != nil {
		return l.groupIDs(account)
	}
	return account.GroupIds()
}

// Credential returns the identity to run a child as: the user's UID, the
// group's GID (the user's primary group when group is empty, and a numeric GID
// when no group has that name), and the user's supplementary groups. It
// refuses UID 0, GID 0 and membership of group 0, so a misconfigured service
// account can never hand a command root's files.
func Credential(username, group string) (*syscall.Credential, error) {
	return lookup{}.credential(username, group)
}

func (l lookup) credential(username, group string) (*syscall.Credential, error) {
	account, err := l.lookupUser(username)
	if err != nil {
		return nil, fmt.Errorf("look up service user %q: %w", username, err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return nil, fmt.Errorf("the service user %q must have a non-root numeric UID", account.Username)
	}
	gid, err := l.groupID(group, account.Gid)
	if err != nil {
		return nil, err
	}
	if gid == 0 {
		return nil, fmt.Errorf("the service group %q must have a non-root numeric GID", group)
	}
	groups, err := l.supplementaryGroups(account)
	if err != nil {
		return nil, err
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: gid, Groups: groups}, nil
}

func (l lookup) supplementaryGroups(account *user.User) ([]uint32, error) {
	ids, err := l.supplementary(account)
	if err != nil {
		return nil, fmt.Errorf("look up groups for service user %q: %w", account.Username, err)
	}
	groups := make([]uint32, 0, len(ids))
	for _, id := range ids {
		parsed, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid supplementary group ID %q for service user %q", id, account.Username)
		}
		if parsed == 0 {
			return nil, fmt.Errorf("the service user %q belongs to the root group", account.Username)
		}
		groups = append(groups, uint32(parsed))
	}
	return groups, nil
}

func (l lookup) groupID(name, fallbackGID string) (uint32, error) {
	if name == "" {
		name = fallbackGID
	}
	group, err := l.lookupGroup(name)
	if err == nil {
		id, parseErr := strconv.ParseUint(group.Gid, 10, 32)
		if parseErr != nil {
			return 0, fmt.Errorf("invalid group ID %q for service group %q", group.Gid, name)
		}
		return uint32(id), nil
	}
	if id, parseErr := strconv.ParseUint(name, 10, 32); parseErr == nil {
		return uint32(id), nil
	}
	return 0, fmt.Errorf("look up service group %q: %w", name, err)
}

// WithEnvironment replaces the given settings in an environment, leaving every
// other entry in place. Settings with an empty value are not set and do not
// remove an existing entry. The new entries follow the rest in name order.
func WithEnvironment(env []string, set map[string]string) []string {
	filtered := make([]string, 0, len(env)+len(set))
	for _, item := range env {
		if name, _, _ := strings.Cut(item, "="); set[name] == "" {
			filtered = append(filtered, item)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(set)) {
		if set[name] != "" {
			filtered = append(filtered, name+"="+set[name])
		}
	}
	return filtered
}

// HasHelpFlag reports whether args ask for help the way the flag package
// understands it. Help needs no particular identity, so it is never re-run.
// Arguments after "--" are operands, not flags.
func HasHelpFlag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "--h", "-help", "--help":
			return true
		}
	}
	return false
}

// RefuseRoot stops a command that writes the instance's files when it would
// run as root, which happens when Reexec found no installed service to take
// an account from. euid is the effective UID, normally os.Geteuid(); hint
// shows how to run the command instead, for example "sudo -u app env
// APP_DATA_DIR=/var/lib/app app backup".
func RefuseRoot(euid int, command, hint string) error {
	if euid != 0 {
		return nil
	}
	return fmt.Errorf("%s writes the instance's files and must not run as root; run it as the service account, for example: %s", command, hint)
}
