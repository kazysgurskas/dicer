// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package guest

import (
	"fmt"
	"maps"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// User is who a process in the guest runs as: the credential it gets, and
// the home directory its HOME is set to. The zero User runs a process as
// whoever starts it.
type User struct {
	Credential *syscall.Credential
	Home       string
}

// UserNamed returns who spec names, as user, uid, user:group or uid:gid,
// looked up in the guest's /etc/passwd and /etc/group. A user gets the
// groups /etc/group gives it. A uid or gid with no entry is taken as it is,
// and a uid with no entry runs with gid 0, no other groups and HOME /, as
// Docker runs it. A name with no entry is an error.
func UserNamed(spec string) (User, error) {
	name, group, hasGroup := strings.Cut(spec, ":")
	u := User{Credential: &syscall.Credential{}, Home: "/"}

	entry, err := userNamed(name)
	uid, isUID := idOf(name)
	switch {
	case err == nil:
		u.Credential.Uid, _ = idOf(entry.Uid)
		u.Credential.Gid, _ = idOf(entry.Gid)
		u.Home = entry.HomeDir
		if gids, err := entry.GroupIds(); err == nil {
			for _, gid := range gids {
				if id, ok := idOf(gid); ok {
					u.Credential.Groups = append(u.Credential.Groups, id)
				}
			}
		}
	case isUID:
		u.Credential.Uid = uid
	default:
		return User{}, fmt.Errorf("no user %q in the guest's /etc/passwd", name)
	}

	if hasGroup {
		gid, err := gidOf(group)
		if err != nil {
			return User{}, err
		}
		u.Credential.Gid = gid
	}
	return u, nil
}

// EnvWithHome returns env with HOME set to the user's home directory, unless
// env sets HOME itself.
func (u User) EnvWithHome(env map[string]string) map[string]string {
	if _, ok := env["HOME"]; ok {
		return env
	}
	out := maps.Clone(env)
	if out == nil {
		out = make(map[string]string, 1)
	}
	out["HOME"] = u.Home
	return out
}

// userNamed returns the /etc/passwd entry of the user name, which may be a
// uid.
func userNamed(name string) (*user.User, error) {
	if _, ok := idOf(name); ok {
		return user.LookupId(name)
	}
	return user.Lookup(name)
}

// gidOf returns the gid of group, which may be a gid itself, looked up in
// /etc/group.
func gidOf(group string) (uint32, error) {
	if id, ok := idOf(group); ok {
		return id, nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return 0, fmt.Errorf("no group %q in the guest's /etc/group", group)
	}
	id, _ := idOf(g.Gid)
	return id, nil
}

// idOf returns s as a uid or gid, and whether it is one.
func idOf(s string) (uint32, bool) {
	id, err := strconv.ParseUint(s, 10, 32)
	return uint32(id), err == nil
}
